package app

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/samber/do/v2"
	"google.golang.org/protobuf/proto"

	"agent-compose/internal/projects"
	"agent-compose/pkg/agentcompose/api"
	"agent-compose/pkg/compose"
	appconfig "agent-compose/pkg/config"
	"agent-compose/pkg/internal/testutil"
	"agent-compose/pkg/volumes"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
	"agent-compose/proto/agentcompose/v2/agentcomposev2connect"
)

func TestIntegrationPatchProjectReplacementAndConcurrency(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:  root,
		DbAddr:    filepath.Join(root, "data.db"),
		DbTimeout: 5 * time.Second,
	}
	di := do.New()
	do.ProvideValue(di, ctx)
	do.ProvideValue(di, config)
	store, err := testutil.OpenConfigStore(t, di)
	if err != nil {
		t.Fatalf("open migrated config store: %v", err)
	}
	controller := projects.NewController(projects.ControllerDependencies{
		Config: config, Store: store, Volumes: volumes.NewManager(store),
	})
	handler := api.NewProjectHandler(projectControllerDelegate{controller: controller}, store, nil, nil)
	_, connectHandler := agentcomposev2connect.NewProjectServiceHandler(handler)
	server := httptest.NewServer(connectHandler)
	t.Cleanup(server.Close)
	client := agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL)

	created, err := client.ApplyProject(ctx, connect.NewRequest(&agentcomposev2.ApplyProjectRequest{
		Spec: &agentcomposev2.ProjectSpec{
			Name: "patch-integration",
			Variables: []*agentcomposev2.EnvVarSpec{
				{Name: "PUBLIC", Value: "before"},
				{Name: "TOKEN", Value: "stored-secret", Secret: true},
			},
		},
		Source: &agentcomposev2.ProjectSource{ComposePath: "/srv/project/compose.yaml"},
	}))
	if err != nil {
		t.Fatalf("ApplyProject create: %v", err)
	}
	if !created.Msg.GetApplied() || created.Msg.GetRevision().GetRevision() != 1 {
		t.Fatalf("ApplyProject create response = %#v", created.Msg)
	}
	projectID := created.Msg.GetProject().GetSummary().GetProjectId()
	baseHash := created.Msg.GetRevision().GetSpecHash()
	projectRef := &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: projectID}}

	loaded, err := client.GetProject(ctx, connect.NewRequest(&agentcomposev2.GetProjectRequest{
		Project: projectRef, IncludeSpec: true,
	}))
	if err != nil {
		t.Fatalf("GetProject after create: %v", err)
	}
	if got := projectVariableValue(loaded.Msg.GetProject().GetSpec(), "TOKEN"); got != "********" {
		t.Fatalf("GetProject secret = %q, want redaction marker", got)
	}

	candidate := proto.Clone(loaded.Msg.GetProject().GetSpec()).(*agentcomposev2.ProjectSpec)
	setProjectVariableValue(t, candidate, "PUBLIC", "after")
	dryRun, err := client.PatchProject(ctx, connect.NewRequest(&agentcomposev2.PatchProjectRequest{
		Project: projectRef, ExpectedCurrentSpecHash: baseHash, Spec: candidate, DryRun: true,
	}))
	if err != nil {
		t.Fatalf("PatchProject dry run: %v", err)
	}
	if dryRun.Msg.GetApplied() || dryRun.Msg.GetRevision().GetSpecHash() == baseHash {
		t.Fatalf("PatchProject dry-run response = %#v", dryRun.Msg)
	}
	unchanged, err := store.GetProject(ctx, projectID)
	if err != nil {
		t.Fatalf("load project after dry run: %v", err)
	}
	if unchanged.CurrentRevision != 1 || unchanged.SpecHash != baseHash {
		t.Fatalf("dry run persisted project = %#v", unchanged)
	}

	_, err = client.PatchProject(ctx, connect.NewRequest(&agentcomposev2.PatchProjectRequest{
		Project: projectRef, ExpectedCurrentSpecHash: "sha256:stale", Spec: candidate,
	}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("PatchProject stale hash error = %v, want ABORTED", err)
	}

	renamed := proto.Clone(candidate).(*agentcomposev2.ProjectSpec)
	renamed.Name = "renamed"
	renameResult, err := client.PatchProject(ctx, connect.NewRequest(&agentcomposev2.PatchProjectRequest{
		Project: projectRef, ExpectedCurrentSpecHash: baseHash, Spec: renamed,
	}))
	if err != nil {
		t.Fatalf("PatchProject rename: %v", err)
	}
	if len(renameResult.Msg.GetIssues()) != 1 || renameResult.Msg.GetIssues()[0].GetPath() != "spec.name" || renameResult.Msg.GetApplied() {
		t.Fatalf("PatchProject rename response = %#v", renameResult.Msg)
	}

	patched, err := client.PatchProject(ctx, connect.NewRequest(&agentcomposev2.PatchProjectRequest{
		Project: projectRef, ExpectedCurrentSpecHash: baseHash, Spec: candidate,
	}))
	if err != nil {
		t.Fatalf("PatchProject apply: %v", err)
	}
	if !patched.Msg.GetApplied() || patched.Msg.GetRevision().GetRevision() != 2 {
		t.Fatalf("PatchProject apply response = %#v", patched.Msg)
	}
	persisted, err := store.GetProject(ctx, projectID)
	if err != nil {
		t.Fatalf("load patched project: %v", err)
	}
	if persisted.SourcePath != "/srv/project/compose.yaml" || persisted.CurrentRevision != 2 {
		t.Fatalf("patched project identity/source = %#v", persisted)
	}
	revision, err := store.GetProjectRevision(ctx, projectID, persisted.CurrentRevision)
	if err != nil {
		t.Fatalf("load patched revision: %v", err)
	}
	if strings.Contains(revision.SpecJSON, "********") || !strings.Contains(revision.SpecJSON, "stored-secret") || !strings.Contains(revision.SpecJSON, `"after"`) {
		t.Fatalf("patched revision = %s", revision.SpecJSON)
	}

	withoutSecret := proto.Clone(candidate).(*agentcomposev2.ProjectSpec)
	withoutSecret.Variables = withoutSecret.Variables[:1]
	deleted, err := client.PatchProject(ctx, connect.NewRequest(&agentcomposev2.PatchProjectRequest{
		Project: projectRef, ExpectedCurrentSpecHash: patched.Msg.GetRevision().GetSpecHash(), Spec: withoutSecret,
	}))
	if err != nil {
		t.Fatalf("PatchProject delete omitted secret: %v", err)
	}
	deletedRevision, err := store.GetProjectRevision(ctx, projectID, int64(deleted.Msg.GetRevision().GetRevision()))
	if err != nil {
		t.Fatalf("load deletion revision: %v", err)
	}
	deletedSpec, err := compose.ParseCanonicalJSON([]byte(deletedRevision.SpecJSON))
	if err != nil {
		t.Fatalf("parse deletion revision: %v", err)
	}
	if _, found := deletedSpec.Variables["TOKEN"]; found {
		t.Fatalf("omitted secret was retained: %#v", deletedSpec.Variables)
	}

	applyUpdate, err := client.ApplyProject(ctx, connect.NewRequest(&agentcomposev2.ApplyProjectRequest{
		Spec: &agentcomposev2.ProjectSpec{
			Name:      "patch-integration",
			Variables: []*agentcomposev2.EnvVarSpec{{Name: "PUBLIC", Value: "apply-still-replaces"}},
		},
		Source: &agentcomposev2.ProjectSource{ComposePath: "/srv/project/compose.yaml"},
	}))
	if err != nil {
		t.Fatalf("ApplyProject existing-project replacement: %v", err)
	}
	if !applyUpdate.Msg.GetApplied() || applyUpdate.Msg.GetRevision().GetRevision() != 4 {
		t.Fatalf("ApplyProject existing-project response = %#v", applyUpdate.Msg)
	}
}

func projectVariableValue(spec *agentcomposev2.ProjectSpec, name string) string {
	for _, variable := range spec.GetVariables() {
		if variable.GetName() == name {
			return variable.GetValue()
		}
	}
	return ""
}

func setProjectVariableValue(t *testing.T, spec *agentcomposev2.ProjectSpec, name, value string) {
	t.Helper()
	for _, variable := range spec.GetVariables() {
		if variable.GetName() == name {
			variable.Value = value
			return
		}
	}
	t.Fatalf("project variable %s not found", name)
}

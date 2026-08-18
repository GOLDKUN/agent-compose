package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-compose/pkg/compose"
	driverpkg "agent-compose/pkg/driver"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/storage/configstore"
	"agent-compose/pkg/workspaces"
)

// createNativeTestSchedulerWithWorkspace mirrors createNativeTestScheduler
// but attaches a full inline agent.Workspace spec (provider/url/path/target)
// instead of a bare {Name: workspaceID} label, matching what project apply
// actually produces for a yaml `workspace:` block (see pkg/compose
// normalizeInlineWorkspaceSpec, which always sets Provider).
func createNativeTestSchedulerWithWorkspace(t testing.TB, ctx context.Context, store *configstore.ConfigStore, scheduler domain.Scheduler, sourcePath string, workspace *compose.WorkspaceSpec) domain.Scheduler {
	t.Helper()
	projectID := "test-project-" + scheduler.Summary.ID
	agentName := "worker"
	agent := compose.NormalizedAgentSpec{
		Name: agentName, Enabled: true, Provider: scheduler.Summary.DefaultAgent, Image: scheduler.Summary.GuestImage,
		CapsetIDs: append([]string(nil), scheduler.Summary.CapsetIDs...),
		Workspace: workspace,
		Scheduler: &compose.NormalizedSchedulerSpec{
			Enabled: scheduler.Summary.Enabled, SandboxPolicy: scheduler.Summary.SandboxPolicy,
			ConcurrencyPolicy: scheduler.Summary.ConcurrencyPolicy, DisplayName: scheduler.Summary.Name,
			Description: scheduler.Summary.Description, Script: scheduler.Script,
		},
	}
	if driver := strings.TrimSpace(scheduler.Summary.Driver); driver != "" {
		agent.Driver = &compose.NormalizedDriverSpec{Name: driver}
	}
	specJSON, err := (&compose.NormalizedProjectSpec{Name: "test-project", Agents: []compose.NormalizedAgentSpec{agent}}).MarshalCanonicalJSON(false)
	if err != nil {
		t.Fatalf("marshal native scheduler fixture: %v", err)
	}
	project, err := store.UpsertProject(ctx, domain.ProjectRecord{ID: projectID, Name: "test-project", SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("upsert native scheduler project: %v", err)
	}
	revision, _, err := store.SaveProjectRevision(ctx, domain.ProjectRevisionRecord{ProjectID: project.ID, SpecHash: "fixture-" + scheduler.Summary.ID, SpecJSON: string(specJSON)})
	if err != nil {
		t.Fatalf("save native scheduler revision: %v", err)
	}
	if _, err := store.UpsertProjectAgent(ctx, domain.ProjectAgentRecord{ProjectID: project.ID, AgentName: agentName, Revision: revision.Revision, SchedulerEnabled: true, SpecJSON: "{}"}); err != nil {
		t.Fatalf("upsert scheduler project agent: %v", err)
	}
	if _, err := store.UpsertProjectScheduler(ctx, domain.ProjectSchedulerRecord{ID: scheduler.Summary.ID, ProjectID: project.ID, SchedulerID: scheduler.Summary.ID, AgentName: agentName, Revision: revision.Revision, Enabled: scheduler.Summary.Enabled, TriggerCount: len(scheduler.Triggers), SpecJSON: "{}"}); err != nil {
		t.Fatalf("upsert native scheduler: %v", err)
	}
	if _, err := store.ReplaceSchedulerTriggers(ctx, scheduler.Summary.ID, scheduler.Triggers); err != nil {
		t.Fatalf("replace native scheduler triggers: %v", err)
	}
	loaded, err := store.GetScheduler(ctx, scheduler.Summary.ID)
	if err != nil {
		t.Fatalf("load native scheduler: %v", err)
	}
	return loaded
}

// TestSchedulerSandboxRunnerEnsureResolvesInlineGitWorkspace reproduces
// issue #599: an agent-compose.yaml agent declares a scheduler-enabled
// `workspace: {provider: git, url: ...}` and the scheduler is run via a path
// that used to look the yaml `name` label up as a workspace_config preset id
// (scheduler.shell/scheduler.exec, or scheduler.agent with a session
// override). Since project apply never creates such a preset, Ensure used to
// fail with "workspace config <id> not found" even though the full workspace
// spec was declared in yaml. Ensure must now resolve the git workspace
// directly from the inline spec.
func TestSchedulerSandboxRunnerEnsureResolvesInlineGitWorkspace(t *testing.T) {
	ctx := context.Background()
	bridge, driver := newTestSandboxRPCBridge(t)
	ensurer := &recordingSchedulerWorkspaceEnsurer{}
	publisher := &schedulerSessionPublisherFake{}
	runner := NewSchedulerSandboxRunner(bridge.config, bridge.store, bridge.configDB, ensurer, driver, nil, nil, bridge.streams, publisher, nil, bridge.agentExecutor)

	scheduler := domain.Scheduler{Summary: domain.SchedulerSummary{
		ID:            "scheduler-inline-git",
		Name:          "Scheduler Inline Git",
		Driver:        driverpkg.RuntimeDriverDocker,
		SandboxPolicy: domain.SchedulerSandboxPolicySticky,
	}}
	scheduler = createNativeTestSchedulerWithWorkspace(t, ctx, bridge.configDB, scheduler, "", &compose.WorkspaceSpec{
		Provider: "git",
		URL:      "https://example.com/some/repo.git",
		Name:     "my-repo",
	})

	sandbox, eventType, err := runner.Ensure(ctx, scheduler, domain.SchedulerAgentRequest{BindingTriggerID: "trigger-inline-git"}, false)
	if err != nil {
		t.Fatalf("Ensure returned error: %v, want the inline git workspace to resolve without a workspace_config lookup", err)
	}
	if eventType != "scheduler.sandbox.created" {
		t.Fatalf("Ensure event type = %q, want scheduler.sandbox.created", eventType)
	}
	if len(ensurer.initialWorkspaces) != 1 || ensurer.initialWorkspaces[0] == nil {
		t.Fatalf("workspace Ensure snapshot = %#v, want one non-nil snapshot", ensurer.initialWorkspaces)
	}
	snapshot := ensurer.initialWorkspaces[0]
	if snapshot.Type != "git" {
		t.Fatalf("resolved workspace type = %q, want git", snapshot.Type)
	}
	var decoded workspaces.GitWorkspaceConfig
	if err := json.Unmarshal([]byte(snapshot.ConfigJSON), &decoded); err != nil {
		t.Fatalf("decode resolved git workspace config: %v", err)
	}
	if decoded.URL != "https://example.com/some/repo.git" {
		t.Fatalf("resolved git workspace url = %q, want https://example.com/some/repo.git", decoded.URL)
	}
	if sandbox.Summary.ID == "" {
		t.Fatalf("expected sandbox to be created")
	}
}

// TestSchedulerSandboxRunnerEnsureResolvesInlineFileWorkspace covers the
// `provider: file` variant of issue #599: the agent's local workspace
// content must be materialized directly from the project source path
// instead of being looked up as a workspace_config preset.
func TestSchedulerSandboxRunnerEnsureResolvesInlineFileWorkspace(t *testing.T) {
	ctx := context.Background()
	bridge, driver := newTestSandboxRPCBridge(t)
	ensurer := &recordingSchedulerWorkspaceEnsurer{}
	publisher := &schedulerSessionPublisherFake{}
	runner := NewSchedulerSandboxRunner(bridge.config, bridge.store, bridge.configDB, ensurer, driver, nil, nil, bridge.streams, publisher, nil, bridge.agentExecutor)

	projectSource := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectSource, "hello.txt"), []byte("hello from source\n"), 0o644); err != nil {
		t.Fatalf("write source fixture file: %v", err)
	}

	scheduler := domain.Scheduler{Summary: domain.SchedulerSummary{
		ID:            "scheduler-inline-file",
		Name:          "Scheduler Inline File",
		Driver:        driverpkg.RuntimeDriverDocker,
		SandboxPolicy: domain.SchedulerSandboxPolicySticky,
	}}
	scheduler = createNativeTestSchedulerWithWorkspace(t, ctx, bridge.configDB, scheduler, projectSource, &compose.WorkspaceSpec{
		Provider: "file",
		Path:     ".",
		Name:     "my-local-workspace",
	})

	_, eventType, err := runner.Ensure(ctx, scheduler, domain.SchedulerAgentRequest{BindingTriggerID: "trigger-inline-file"}, false)
	if err != nil {
		t.Fatalf("Ensure returned error: %v, want the inline file workspace to materialize without a workspace_config lookup", err)
	}
	if eventType != "scheduler.sandbox.created" {
		t.Fatalf("Ensure event type = %q, want scheduler.sandbox.created", eventType)
	}
	if len(ensurer.initialWorkspaces) != 1 || ensurer.initialWorkspaces[0] == nil {
		t.Fatalf("workspace Ensure snapshot = %#v, want one non-nil snapshot", ensurer.initialWorkspaces)
	}
	snapshot := ensurer.initialWorkspaces[0]
	if snapshot.Type != "file" {
		t.Fatalf("resolved workspace type = %q, want file", snapshot.Type)
	}
	contentRoot, err := workspaces.FileWorkspaceContentRoot(bridge.config, domain.WorkspaceConfig{ID: snapshot.ID, Type: "file", ConfigJSON: snapshot.ConfigJSON})
	if err != nil {
		t.Fatalf("resolve materialized file workspace content root: %v", err)
	}
	copied, err := os.ReadFile(filepath.Join(contentRoot, "hello.txt"))
	if err != nil {
		t.Fatalf("read materialized workspace content: %v", err)
	}
	if string(copied) != "hello from source\n" {
		t.Fatalf("materialized workspace content = %q, want copied source content", copied)
	}
}

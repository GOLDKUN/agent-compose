package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/samber/do/v2"

	appconfig "agent-compose/pkg/config"
	"agent-compose/pkg/internal/testutil"
	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
	"agent-compose/proto/agentcompose/v2/agentcomposev2connect"
)

func TestIntegrationBatchGetLatestSchedulerRunsFindsRunBeyondFirstPage(t *testing.T) {
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

	project, err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID:         "project-regression",
		Name:       "Scheduler run regression",
		SourcePath: "/tmp/scheduler-run-regression",
		SourceJSON: `{"kind":"local"}`,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	revision, _, err := store.SaveProjectRevision(ctx, domain.ProjectRevisionRecord{
		ProjectID: project.ID,
		SpecHash:  "regression-spec",
		SpecJSON:  `{"agents":[]}`,
	})
	if err != nil {
		t.Fatalf("create project revision: %v", err)
	}
	const (
		loaderID    = "loader-regression"
		schedulerID = "scheduler-regression"
		agentName   = "reviewer"
		targetRunID = "run-target-beyond-500"
		targetID    = "sandbox-target"
		missingID   = "sandbox-missing"
	)
	if _, err := store.UpsertProjectAgent(ctx, domain.ProjectAgentRecord{
		ProjectID:        project.ID,
		AgentName:        agentName,
		Revision:         revision.Revision,
		SchedulerEnabled: true,
		SpecJSON:         `{"name":"reviewer"}`,
	}); err != nil {
		t.Fatalf("create project agent: %v", err)
	}
	if _, err := store.UpsertManagedLoader(ctx, domain.Loader{
		Summary: domain.LoaderSummary{
			ID:                 loaderID,
			Name:               "Regression scheduler",
			Runtime:            domain.LoaderRuntimeScheduler,
			ManagedProjectID:   project.ID,
			ManagedRevision:    revision.Revision,
			ManagedAgentName:   agentName,
			ManagedSchedulerID: schedulerID,
		},
		Script: "function main() {}",
	}); err != nil {
		t.Fatalf("create managed loader: %v", err)
	}
	if _, err := store.UpsertProjectScheduler(ctx, domain.ProjectSchedulerRecord{
		ProjectID:       project.ID,
		SchedulerID:     schedulerID,
		AgentName:       agentName,
		ManagedLoaderID: loaderID,
		Revision:        revision.Revision,
		Enabled:         true,
		TriggerCount:    1,
		SpecJSON:        `{"id":"scheduler-regression"}`,
	}); err != nil {
		t.Fatalf("create project scheduler: %v", err)
	}

	startedAt := time.UnixMilli(1_720_000_000_000).UTC()
	if err := store.CreateLoaderRun(ctx, domain.LoaderRunSummary{
		ID: targetRunID, LoaderID: loaderID, TriggerID: "trigger-regression",
		Status: domain.LoaderRunStatusSucceeded, StartedAt: startedAt,
	}); err != nil {
		t.Fatalf("create target scheduler run: %v", err)
	}
	if err := store.AddLoaderEvent(ctx, domain.LoaderEvent{
		ID: "event-target", LoaderID: loaderID, RunID: targetRunID,
		TriggerID: "trigger-regression", Type: "loader.agent.completed",
		LinkedSandboxID: targetID, CreatedAt: startedAt,
	}); err != nil {
		t.Fatalf("link target scheduler run to sandbox: %v", err)
	}
	for index := 0; index < 501; index++ {
		runID := fmt.Sprintf("run-newer-%03d", index)
		if err := store.CreateLoaderRun(ctx, domain.LoaderRunSummary{
			ID: runID, LoaderID: loaderID, TriggerID: "trigger-regression",
			Status: domain.LoaderRunStatusSucceeded, StartedAt: startedAt.Add(time.Duration(index+1) * time.Second),
		}); err != nil {
			t.Fatalf("create newer scheduler run %s: %v", runID, err)
		}
	}

	handler := NewProjectHandler(nil, store, nil)
	mux := http.NewServeMux()
	path, connectHandler := agentcomposev2connect.NewProjectServiceHandler(handler)
	mux.Handle(path, connectHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL)
	projectRef := &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: project.ID}}

	page, err := client.ListSchedulerRuns(ctx, connect.NewRequest(&agentcomposev2.ListSchedulerRunsRequest{
		Project: projectRef,
		Limit:   500,
	}))
	if err != nil {
		t.Fatalf("list first 500 scheduler runs: %v", err)
	}
	const totalRuns = 502
	if len(page.Msg.GetRuns()) != 500 || page.Msg.GetTotal() != totalRuns {
		t.Fatalf("first page has %d runs and total %d, want 500 runs and total %d", len(page.Msg.GetRuns()), page.Msg.GetTotal(), totalRuns)
	}
	for _, run := range page.Msg.GetRuns() {
		if run.GetRunId() == targetRunID {
			t.Fatalf("target run %q unexpectedly appeared in first 500 runs", targetRunID)
		}
	}

	batch, err := client.BatchGetLatestSchedulerRuns(ctx, connect.NewRequest(&agentcomposev2.BatchGetLatestSchedulerRunsRequest{
		Project:    projectRef,
		SandboxIds: []string{missingID, targetID, targetID},
	}))
	if err != nil {
		t.Fatalf("batch get latest scheduler runs: %v", err)
	}
	results := batch.Msg.GetResults()
	if len(results) != 2 {
		t.Fatalf("batch results count = %d, want 2 distinct sandbox results", len(results))
	}
	if results[0].GetSandboxId() != missingID || results[0].GetRun() != nil {
		t.Fatalf("first batch result = %#v, want ordered missing sandbox result", results[0])
	}
	target := results[1]
	if target.GetSandboxId() != targetID || target.GetRun().GetRunId() != targetRunID {
		t.Fatalf("target batch result = %#v, want sandbox %q run %q", target, targetID, targetRunID)
	}
	if target.GetRun().GetProjectId() != project.ID || target.GetRun().GetSchedulerId() != schedulerID || target.GetRun().GetAgentName() != agentName {
		t.Fatalf("target scheduler identity = %#v", target.GetRun())
	}
}

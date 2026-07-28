package api

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/schedulers"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestProjectHandlerRunSchedulerSupportsMainAndTerminalStatuses(t *testing.T) {
	store, runtime, handler := newSchedulerRunHandlerFixture()
	if store.scheduler.Enabled {
		t.Fatal("fixture scheduler must be disabled to exercise manual execution")
	}
	tests := []struct {
		name       string
		triggerID  string
		status     string
		wantStatus agentcomposev2.SchedulerRunStatus
	}{
		{name: "succeeded", triggerID: "trigger-1", status: domain.SchedulerRunStatusSucceeded, wantStatus: agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_SUCCEEDED},
		{name: "canceled", triggerID: "trigger-1", status: domain.SchedulerRunStatusCanceled, wantStatus: agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_CANCELED},
		{name: "skipped", triggerID: "trigger-1", status: domain.SchedulerRunStatusSkipped, wantStatus: agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_SKIPPED},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime.runResult = domain.SchedulerRunSummary{
				ID:          "run-" + test.name,
				SchedulerID: store.scheduler.ID,
				TriggerID:   test.triggerID,
				Status:      test.status,
				StartedAt:   time.Unix(100, 0).UTC(),
				PayloadJSON: `{"value":true}`,
			}
			response, err := handler.RunScheduler(context.Background(), connect.NewRequest(&agentcomposev2.RunSchedulerRequest{
				Project:     &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}},
				AgentName:   store.scheduler.AgentName,
				TriggerId:   test.triggerID,
				PayloadJson: ` { "value" : true } `,
			}))
			if err != nil {
				t.Fatalf("RunScheduler returned error: %v", err)
			}
			if response.Msg.GetRun().GetStatus() != test.wantStatus || response.Msg.GetRun().GetProjectId() != store.project.ID || response.Msg.GetRun().GetAgentName() != store.scheduler.AgentName {
				t.Fatalf("response run = %#v", response.Msg.GetRun())
			}
			if runtime.lastRequest.SchedulerID != store.scheduler.ID || runtime.lastRequest.TriggerID != test.triggerID || runtime.lastRequest.PayloadJSON != `{"value":true}` {
				t.Fatalf("runtime request = %#v", runtime.lastRequest)
			}
		})
	}
}

func TestProjectHandlerRunSchedulerValidatesPayloadAndMissingTrigger(t *testing.T) {
	store, runtime, handler := newSchedulerRunHandlerFixture()
	request := &agentcomposev2.RunSchedulerRequest{
		Project:   &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}},
		AgentName: store.scheduler.AgentName,
	}
	request.PayloadJson = `{bad`
	if _, err := handler.RunScheduler(context.Background(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid payload code=%v err=%v", connect.CodeOf(err), err)
	}
	if runtime.runCalls != 0 {
		t.Fatalf("runtime calls after invalid payload = %d", runtime.runCalls)
	}

	request.PayloadJson = `{}`
	request.TriggerId = "missing"
	runtime.runErr = domain.ResourceError(domain.ErrNotFound, "loader trigger", "missing", "loader trigger missing not found", nil)
	if _, err := handler.RunScheduler(context.Background(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("missing trigger code=%v err=%v", connect.CodeOf(err), err)
	}
}

func TestProjectHandlerInvokeSchedulerReturnsValueWithoutRunResource(t *testing.T) {
	store, runtime, handler := newSchedulerRunHandlerFixture()
	runtime.invokeResult = schedulers.InvocationResult{ResultJSON: `{"ok":true}`, DurationMs: 42, Warnings: []string{"warning"}}
	response, err := handler.InvokeScheduler(context.Background(), connect.NewRequest(&agentcomposev2.InvokeSchedulerRequest{
		Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}}, AgentName: store.scheduler.AgentName, PayloadJson: ` { "value" : true } `,
	}))
	if err != nil || response.Msg.GetResultJson() != `{"ok":true}` || response.Msg.GetDurationMs() != 42 || len(response.Msg.GetWarnings()) != 1 {
		t.Fatalf("InvokeScheduler response=%#v err=%v", response, err)
	}
	if runtime.invokeLoaderID != store.scheduler.ID || runtime.invokePayload != `{"value":true}` {
		t.Fatalf("invocation loader/payload=%q/%q", runtime.invokeLoaderID, runtime.invokePayload)
	}
	store.scheduler.SpecJSON = `{"triggers":[{"name":"nightly"}]}`
	if _, err := handler.InvokeScheduler(context.Background(), connect.NewRequest(&agentcomposev2.InvokeSchedulerRequest{
		Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}}, AgentName: store.scheduler.AgentName,
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("declarative invoke code=%v err=%v", connect.CodeOf(err), err)
	}
}

func TestProjectHandlerSchedulerRunLifecycle(t *testing.T) {
	store, runtime, handler := newSchedulerRunHandlerFixture()
	startedAt := time.Unix(200, 0).UTC()
	runtime.startResult = domain.SchedulerRunSummary{ID: "run-start", SchedulerID: store.scheduler.ID, TriggerID: "trigger-1", Status: domain.SchedulerRunStatusRunning, StartedAt: startedAt}
	started, err := handler.StartSchedulerRun(context.Background(), connect.NewRequest(&agentcomposev2.StartSchedulerRunRequest{
		Project:     &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}},
		AgentName:   store.scheduler.AgentName,
		TriggerId:   "trigger-1",
		PayloadJson: `{"start":true}`,
	}))
	if err != nil || started.Msg.GetRun().GetStatus() != agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_RUNNING {
		t.Fatalf("StartSchedulerRun response=%#v err=%v", started, err)
	}

	getRun := domain.SchedulerRunSummary{ID: "run-get", SchedulerID: store.scheduler.ID, TriggerID: "trigger-1", Status: domain.SchedulerRunStatusCanceled, Error: "user stop", StartedAt: startedAt}
	store.runs = []domain.SchedulerRunSummary{getRun}
	runtime.getResult = getRun
	got, err := handler.GetSchedulerRun(context.Background(), connect.NewRequest(&agentcomposev2.GetSchedulerRunRequest{
		Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}},
		RunId:   getRun.ID,
	}))
	if err != nil || got.Msg.GetRun().GetStatus() != agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_CANCELED || got.Msg.GetRun().GetError() != "user stop" {
		t.Fatalf("GetSchedulerRun response=%#v err=%v", got, err)
	}

	newer := domain.SchedulerRunSummary{ID: "run-new", SchedulerID: store.scheduler.ID, TriggerID: "trigger-1", Status: domain.SchedulerRunStatusSucceeded, StartedAt: startedAt.Add(time.Second)}
	older := domain.SchedulerRunSummary{ID: "run-old", SchedulerID: store.scheduler.ID, TriggerID: "trigger-1", Status: domain.SchedulerRunStatusSkipped, StartedAt: startedAt}
	store.runs = []domain.SchedulerRunSummary{newer, older}
	store.sandboxIDs = map[schedulers.SchedulerRunKey][]string{{SchedulerID: newer.SchedulerID, RunID: newer.ID}: {"sandbox-1"}}
	first, err := handler.ListSchedulerRuns(context.Background(), connect.NewRequest(&agentcomposev2.ListSchedulerRunsRequest{
		Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}},
		Limit:   1,
	}))
	if err != nil || len(first.Msg.GetRuns()) != 1 || first.Msg.GetRuns()[0].GetRunId() != newer.ID || len(first.Msg.GetRuns()[0].GetSandboxIds()) != 1 || first.Msg.GetTotal() != 2 {
		t.Fatalf("ListSchedulerRuns first=%#v err=%v", first, err)
	}
	second, err := handler.ListSchedulerRuns(context.Background(), connect.NewRequest(&agentcomposev2.ListSchedulerRunsRequest{
		Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}},
		Limit:   1,
		Offset:  1,
	}))
	if err != nil || len(second.Msg.GetRuns()) != 1 || second.Msg.GetRuns()[0].GetRunId() != older.ID || second.Msg.GetTotal() != 2 {
		t.Fatalf("ListSchedulerRuns second=%#v err=%v", second, err)
	}
	if _, err := handler.ListSchedulerRuns(context.Background(), connect.NewRequest(&agentcomposev2.ListSchedulerRunsRequest{
		Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}}, TriggerId: "trigger-1", Status: agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_SKIPPED, Limit: 1,
	})); err != nil || !store.lastRunFilter.RequireTrigger || store.lastRunFilter.TriggerID != "trigger-1" || store.lastRunFilter.Status != domain.SchedulerRunStatusSkipped {
		t.Fatalf("ListSchedulerRuns filter=%#v err=%v", store.lastRunFilter, err)
	}

	store.runs = []domain.SchedulerRunSummary{getRun}
	runtime.stopResult = getRun
	runtime.stopRequested = true
	stopped, err := handler.StopSchedulerRun(context.Background(), connect.NewRequest(&agentcomposev2.StopSchedulerRunRequest{
		Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}},
		RunId:   getRun.ID,
		Reason:  "user stop",
	}))
	if err != nil || !stopped.Msg.GetStopRequested() || runtime.stopReason != "user stop" {
		t.Fatalf("StopSchedulerRun response=%#v reason=%q err=%v", stopped, runtime.stopReason, err)
	}
}

func TestProjectHandlerBatchGetsLatestSchedulerRuns(t *testing.T) {
	store, _, handler := newSchedulerRunHandlerFixture()
	run := domain.SchedulerRunSummary{
		ID: "run-associated", SchedulerID: store.scheduler.ID, TriggerID: "trigger-1",
		Status: domain.SchedulerRunStatusSucceeded, StartedAt: time.Unix(200, 0).UTC(),
	}
	store.batchRunsBySandbox = map[string]domain.SchedulerRunSummary{"sandbox-a": run}
	response, err := handler.BatchGetLatestSchedulerRuns(context.Background(), connect.NewRequest(&agentcomposev2.BatchGetLatestSchedulerRunsRequest{
		Project:    &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}},
		SandboxIds: []string{" sandbox-a ", "sandbox-missing", "sandbox-a", ""},
	}))
	if err != nil {
		t.Fatalf("BatchGetLatestSchedulerRuns returned error: %v", err)
	}
	if !slices.Equal(store.lastBatchLoaderIDs, []string{store.scheduler.ID}) ||
		!slices.Equal(store.lastBatchSandboxIDs, []string{"sandbox-a", "sandbox-missing"}) {
		t.Fatalf("batch filters=%#v/%#v", store.lastBatchLoaderIDs, store.lastBatchSandboxIDs)
	}
	results := response.Msg.GetResults()
	if len(results) != 2 || results[0].GetSandboxId() != "sandbox-a" || results[0].GetRun().GetRunId() != run.ID ||
		results[0].GetRun().GetProjectId() != store.project.ID || results[1].GetSandboxId() != "sandbox-missing" || results[1].GetRun() != nil {
		t.Fatalf("batch results=%#v", results)
	}
}

func TestProjectHandlerRejectsExcessiveSchedulerRunBatch(t *testing.T) {
	store, _, handler := newSchedulerRunHandlerFixture()
	sandboxIDs := make([]string, maxSchedulerRunBatchSandboxIDs+1)
	for index := range sandboxIDs {
		sandboxIDs[index] = fmt.Sprintf("sandbox-%d", index)
	}
	_, err := handler.BatchGetLatestSchedulerRuns(context.Background(), connect.NewRequest(&agentcomposev2.BatchGetLatestSchedulerRunsRequest{
		Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}}, SandboxIds: sandboxIDs,
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("excessive batch query code=%v err=%v", connect.CodeOf(err), err)
	}
}

func TestProjectHandlerListsProjectSchedulerEventsWithIdentityAndOffset(t *testing.T) {
	store, _, handler := newSchedulerRunHandlerFixture()
	createdAt := time.Unix(300, 0).UTC()
	store.events = []domain.SchedulerEvent{
		{ID: "event-2", SchedulerID: store.scheduler.ID, RunID: "run-1", TriggerID: "trigger-1", Type: "scheduler.log", LinkedSandboxID: "sandbox-1", CreatedAt: createdAt},
		{ID: "event-1", SchedulerID: store.scheduler.ID, RunID: "run-1", TriggerID: "trigger-1", Type: "scheduler.run.started", CreatedAt: createdAt.Add(-time.Second)},
	}
	first, err := handler.ListProjectSchedulerEvents(context.Background(), connect.NewRequest(&agentcomposev2.ListProjectSchedulerEventsRequest{
		Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}}, Limit: 1,
	}))
	if err != nil || len(first.Msg.GetEvents()) != 1 || first.Msg.GetEvents()[0].GetAgentName() != store.scheduler.AgentName ||
		first.Msg.GetEvents()[0].GetSchedulerId() != store.scheduler.SchedulerID || first.Msg.GetEvents()[0].GetLinkedSandboxId() != "sandbox-1" || first.Msg.GetTotal() != 2 {
		t.Fatalf("first event page=%#v err=%v", first, err)
	}
	second, err := handler.ListProjectSchedulerEvents(context.Background(), connect.NewRequest(&agentcomposev2.ListProjectSchedulerEventsRequest{
		Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}}, Limit: 1, Offset: 1,
	}))
	if err != nil || len(second.Msg.GetEvents()) != 1 || second.Msg.GetEvents()[0].GetId() != "event-1" || second.Msg.GetTotal() != 2 {
		t.Fatalf("second event page=%#v err=%v", second, err)
	}
}

func TestSchedulerRunStatusToProto(t *testing.T) {
	tests := map[string]agentcomposev2.SchedulerRunStatus{
		domain.SchedulerRunStatusRunning:   agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_RUNNING,
		domain.SchedulerRunStatusSucceeded: agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_SUCCEEDED,
		domain.SchedulerRunStatusFailed:    agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_FAILED,
		domain.SchedulerRunStatusCanceled:  agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_CANCELED,
		domain.SchedulerRunStatusSkipped:   agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_SKIPPED,
		"unknown":                          agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_UNSPECIFIED,
	}
	for status, want := range tests {
		if got := schedulerRunStatusToProto(status); got != want {
			t.Fatalf("schedulerRunStatusToProto(%q)=%v, want %v", status, got, want)
		}
	}
}

func TestSchedulerRunTriggerKindToProto(t *testing.T) {
	t.Parallel()

	tests := map[string]agentcomposev2.TriggerKind{
		domain.SchedulerTriggerKindCron:     agentcomposev2.TriggerKind_TRIGGER_KIND_CRON,
		domain.SchedulerTriggerKindInterval: agentcomposev2.TriggerKind_TRIGGER_KIND_INTERVAL,
		domain.SchedulerTriggerKindTimeout:  agentcomposev2.TriggerKind_TRIGGER_KIND_TIMEOUT,
		domain.SchedulerTriggerKindEvent:    agentcomposev2.TriggerKind_TRIGGER_KIND_EVENT,
		"unknown":                           agentcomposev2.TriggerKind_TRIGGER_KIND_UNSPECIFIED,
	}

	for input, want := range tests {
		input, want := input, want
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			got := schedulerRunToProto(
				domain.SchedulerRunSummary{TriggerKind: input},
				domain.ProjectSchedulerRecord{},
			).GetTriggerKind()
			if got != want {
				t.Fatalf("trigger kind = %v, want %v", got, want)
			}
		})
	}
}

func newSchedulerRunHandlerFixture() (*schedulerRunProjectStoreFake, *schedulerRunRuntimeFake, *ProjectHandler) {
	store := &schedulerRunProjectStoreFake{
		project: domain.ProjectRecord{ID: "project-1", Name: "Project", CurrentRevision: 1},
		scheduler: domain.ProjectSchedulerRecord{
			ProjectID:   "project-1",
			AgentName:   "agent-1",
			SchedulerID: "scheduler-1",
			ID:          "loader-1",
			Revision:    1,
			Enabled:     false,
			SpecJSON:    `{"script":"function main() {}"}`,
		},
	}
	runtime := &schedulerRunRuntimeFake{}
	return store, runtime, NewProjectHandler(nil, store, runtime)
}

type schedulerRunProjectStoreFake struct {
	project             domain.ProjectRecord
	scheduler           domain.ProjectSchedulerRecord
	schedulers          []domain.ProjectSchedulerRecord
	runs                []domain.SchedulerRunSummary
	events              []domain.SchedulerEvent
	sandboxIDs          map[schedulers.SchedulerRunKey][]string
	lastRunFilter       schedulers.SchedulerRunPageFilter
	batchRunsBySandbox  map[string]domain.SchedulerRunSummary
	lastBatchLoaderIDs  []string
	lastBatchSandboxIDs []string
}

func (s *schedulerRunProjectStoreFake) ListSchedulerEventsPage(_ context.Context, filter schedulers.SchedulerEventPageFilter) ([]domain.SchedulerEvent, error) {
	items := make([]domain.SchedulerEvent, 0, len(s.events))
	for _, event := range s.events {
		if !slices.Contains(filter.SchedulerIDs, event.SchedulerID) || (filter.RequireTrigger && event.TriggerID == "") ||
			(strings.TrimSpace(filter.TriggerID) != "" && event.TriggerID != filter.TriggerID) || (strings.TrimSpace(filter.RunID) != "" && event.RunID != filter.RunID) {
			continue
		}
		if !filter.BeforeCreatedAt.IsZero() && compareLoaderEventKey(event, filter.BeforeCreatedAt, filter.BeforeSchedulerID, filter.BeforeEventID) >= 0 {
			continue
		}
		if !filter.AfterCreatedAt.IsZero() && compareLoaderEventKey(event, filter.AfterCreatedAt, filter.AfterSchedulerID, filter.AfterEventID) <= 0 {
			continue
		}
		if !filter.FromCreatedAt.IsZero() && compareLoaderEventKey(event, filter.FromCreatedAt, filter.FromSchedulerID, filter.FromEventID) < 0 {
			continue
		}
		if !filter.ThroughCreatedAt.IsZero() && compareLoaderEventKey(event, filter.ThroughCreatedAt, filter.ThroughSchedulerID, filter.ThroughEventID) > 0 {
			continue
		}
		items = append(items, event)
	}
	sort.Slice(items, func(i, j int) bool {
		comparison := compareLoaderEventKey(items[i], items[j].CreatedAt, items[j].SchedulerID, items[j].ID)
		if filter.Ascending {
			return comparison < 0
		}
		return comparison > 0
	})
	start := min(max(filter.Offset, 0), len(items))
	end := min(start+filter.Limit, len(items))
	return append([]domain.SchedulerEvent(nil), items[start:end]...), nil
}

func (s *schedulerRunProjectStoreFake) CountSchedulerEventsPage(_ context.Context, filter schedulers.SchedulerEventPageFilter) (int, error) {
	filter.Offset = 0
	filter.Limit = int(^uint(0) >> 1)
	items, err := s.ListSchedulerEventsPage(context.Background(), filter)
	return len(items), err
}

func compareLoaderEventKey(event domain.SchedulerEvent, createdAt time.Time, loaderID, eventID string) int {
	if !event.CreatedAt.Equal(createdAt) {
		if event.CreatedAt.Before(createdAt) {
			return -1
		}
		return 1
	}
	if comparison := strings.Compare(event.SchedulerID, loaderID); comparison != 0 {
		return comparison
	}
	return strings.Compare(event.ID, eventID)
}

func (s *schedulerRunProjectStoreFake) GetProject(context.Context, string) (domain.ProjectRecord, error) {
	return s.project, nil
}

func (s *schedulerRunProjectStoreFake) ListProjects(context.Context, domain.ProjectListOptions) (domain.ProjectListResult, error) {
	return domain.ProjectListResult{Projects: []domain.ProjectRecord{s.project}}, nil
}

func (s *schedulerRunProjectStoreFake) ListProjectAgents(context.Context, string) ([]domain.ProjectAgentRecord, error) {
	return nil, nil
}

func (s *schedulerRunProjectStoreFake) ListProjectSchedulers(context.Context, string) ([]domain.ProjectSchedulerRecord, error) {
	if s.schedulers != nil {
		return append([]domain.ProjectSchedulerRecord(nil), s.schedulers...), nil
	}
	return []domain.ProjectSchedulerRecord{s.scheduler}, nil
}

func (s *schedulerRunProjectStoreFake) GetProjectRevision(context.Context, string, int64) (domain.ProjectRevisionRecord, error) {
	return domain.ProjectRevisionRecord{}, nil
}

func (s *schedulerRunProjectStoreFake) GetSchedulerRunForSchedulers(_ context.Context, loaderIDs []string, runID string) (domain.SchedulerRunSummary, error) {
	for _, run := range s.runs {
		if run.ID != runID {
			continue
		}
		for _, loaderID := range loaderIDs {
			if run.SchedulerID == loaderID {
				return run, nil
			}
		}
	}
	return domain.SchedulerRunSummary{}, domain.ResourceError(domain.ErrNotFound, "loader run", runID, fmt.Sprintf("loader run %s not found", runID), nil)
}

func (s *schedulerRunProjectStoreFake) ListSchedulerRunsPage(_ context.Context, filter schedulers.SchedulerRunPageFilter) ([]domain.SchedulerRunSummary, error) {
	s.lastRunFilter = filter
	start := min(max(filter.Offset, 0), len(s.runs))
	if filter.BeforeRunID != "" {
		start = len(s.runs)
		for index, run := range s.runs {
			if run.ID == filter.BeforeRunID {
				start = index + 1
				break
			}
		}
	}
	end := min(start+filter.Limit, len(s.runs))
	return append([]domain.SchedulerRunSummary(nil), s.runs[start:end]...), nil
}

func (s *schedulerRunProjectStoreFake) CountSchedulerRunsPage(context.Context, schedulers.SchedulerRunPageFilter) (int, error) {
	return len(s.runs), nil
}

func (s *schedulerRunProjectStoreFake) ListSchedulerRunSandboxIDs(_ context.Context, _ []schedulers.SchedulerRunKey) (map[schedulers.SchedulerRunKey][]string, error) {
	return s.sandboxIDs, nil
}

func (s *schedulerRunProjectStoreFake) BatchGetLatestSchedulerRunsBySandboxIDs(_ context.Context, loaderIDs, sandboxIDs []string) (map[string]domain.SchedulerRunSummary, error) {
	s.lastBatchLoaderIDs = append([]string(nil), loaderIDs...)
	s.lastBatchSandboxIDs = append([]string(nil), sandboxIDs...)
	return s.batchRunsBySandbox, nil
}

type schedulerRunRuntimeFake struct {
	runResult      domain.SchedulerRunSummary
	startResult    domain.SchedulerRunSummary
	getResult      domain.SchedulerRunSummary
	stopResult     domain.SchedulerRunSummary
	runErr         error
	stopRequested  bool
	runCalls       int
	lastRequest    schedulers.SchedulerRunRequest
	stopReason     string
	invokeResult   schedulers.InvocationResult
	invokeLoaderID string
	invokePayload  string
	pruneRequest   schedulers.SchedulerRunPruneRequest
	pruneResult    schedulers.SchedulerRunPruneResult
	pruneErr       error
}

func (f *schedulerRunRuntimeFake) PruneSchedulerRuns(_ context.Context, request schedulers.SchedulerRunPruneRequest) (schedulers.SchedulerRunPruneResult, error) {
	f.pruneRequest = request
	return f.pruneResult, f.pruneErr
}

func (f *schedulerRunRuntimeFake) InvokeScheduler(_ context.Context, loaderID, payloadJSON string) (schedulers.InvocationResult, error) {
	f.invokeLoaderID = loaderID
	f.invokePayload = payloadJSON
	return f.invokeResult, nil
}

func (f *schedulerRunRuntimeFake) SetSchedulerEnabled(context.Context, string, bool) (domain.Scheduler, error) {
	return domain.Scheduler{}, nil
}

func (f *schedulerRunRuntimeFake) SetSchedulerTriggerEnabled(context.Context, string, string, bool) (domain.Scheduler, error) {
	return domain.Scheduler{}, nil
}

func (f *schedulerRunRuntimeFake) RunScheduler(_ context.Context, request schedulers.SchedulerRunRequest) (domain.SchedulerRunSummary, error) {
	f.runCalls++
	f.lastRequest = request
	return f.runResult, f.runErr
}

func (f *schedulerRunRuntimeFake) StartSchedulerRun(_ context.Context, request schedulers.SchedulerRunRequest) (domain.SchedulerRunSummary, error) {
	f.lastRequest = request
	return f.startResult, nil
}

func (f *schedulerRunRuntimeFake) GetSchedulerRun(context.Context, string, string) (domain.SchedulerRunSummary, error) {
	return f.getResult, nil
}

func (f *schedulerRunRuntimeFake) StopSchedulerRun(_ context.Context, _, _, reason string) (domain.SchedulerRunSummary, bool, error) {
	f.stopReason = reason
	return f.stopResult, f.stopRequested, nil
}

package api

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/schedulers"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestNewProjectHandlerAllowsOmittedSchedulerRuntime(t *testing.T) {
	handler := NewProjectHandler(nil, nil)

	if handler.schedulerRuntime != nil || handler.schedulerRuns != nil || handler.invocations != nil || handler.schedulerPrune != nil {
		t.Fatalf("scheduler runtime dependencies = %#v", handler)
	}
}

func TestNewProjectHandlerUsesControllerSchedulerRuns(t *testing.T) {
	controller := schedulers.NewController(schedulers.ControllerDependencies{})

	handler := NewProjectHandler(nil, nil, controller)

	if handler.schedulerRuns != controller.SchedulerRuns() {
		t.Fatalf("scheduler runs = %T, want controller scheduler runs", handler.schedulerRuns)
	}
}

func TestNewProjectHandlerAllowsNilControllerRuntime(t *testing.T) {
	var controller *schedulers.Controller

	handler := NewProjectHandler(nil, nil, controller)

	if handler.schedulerRuntime == nil {
		t.Fatal("scheduler runtime lost typed nil controller")
	}
	if handler.schedulerRuns != nil {
		t.Fatalf("scheduler runs = %T, want nil", handler.schedulerRuns)
	}
}

func TestProjectHandlerSchedulerUpdatesUseSchedulerRuntime(t *testing.T) {
	const (
		projectID   = "project-1"
		agentName   = "中文智能体"
		schedulerID = "scheduler-1"
		triggerID   = "中文心跳"
	)
	store := schedulerRuntimeProjectStore{
		project: domain.ProjectRecord{ID: projectID, Name: "migration"},
		scheduler: domain.ProjectSchedulerRecord{
			ProjectID:   projectID,
			AgentName:   agentName,
			SchedulerID: "scheduler-1",
			ID:          schedulerID,
		},
	}
	runtime := &schedulerRuntimeFake{scheduler: domain.Scheduler{
		Summary:  domain.SchedulerSummary{ID: schedulerID},
		Triggers: []domain.SchedulerTrigger{{ID: triggerID, Kind: domain.SchedulerTriggerKindInterval, IntervalMs: 3000}},
	}}
	handler := NewProjectHandler(nil, store, runtime)

	enabled, err := handler.SetSchedulerEnabled(context.Background(), connect.NewRequest(&agentcomposev2.SetSchedulerEnabledRequest{
		Project:   &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: projectID}},
		AgentName: agentName,
		Enabled:   true,
	}))
	if err != nil {
		t.Fatalf("SetSchedulerEnabled returned error: %v", err)
	}
	if runtime.enabledCalls != 1 || runtime.schedulerID != schedulerID || !enabled.Msg.GetScheduler().GetEnabled() {
		t.Fatalf("scheduler update calls=%d scheduler=%q response=%#v", runtime.enabledCalls, runtime.schedulerID, enabled.Msg.GetScheduler())
	}

	trigger, err := handler.SetSchedulerTriggerEnabled(context.Background(), connect.NewRequest(&agentcomposev2.SetSchedulerTriggerEnabledRequest{
		Project:   &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: projectID}},
		AgentName: agentName,
		TriggerId: triggerID,
		Enabled:   true,
	}))
	if err != nil {
		t.Fatalf("SetSchedulerTriggerEnabled returned error: %v", err)
	}
	if runtime.triggerCalls != 1 || runtime.triggerID != triggerID || !trigger.Msg.GetTrigger().GetEnabled() {
		t.Fatalf("trigger update calls=%d trigger=%q response=%#v", runtime.triggerCalls, runtime.triggerID, trigger.Msg.GetTrigger())
	}
}

func TestProjectHandlerGetSchedulerMapsPersistedNormalizedSpec(t *testing.T) {
	const projectID = "project-1"
	store := schedulerRuntimeProjectStore{
		project: domain.ProjectRecord{ID: projectID, Name: "scheduler-spec"},
		scheduler: domain.ProjectSchedulerRecord{
			ID:          "scheduler-1",
			ProjectID:   projectID,
			AgentName:   "reviewer",
			SchedulerID: "scheduler-1",
			SpecJSON:    `{"enabled":true,"sandbox_policy":"sticky","concurrency_policy":"parallel","display_name":"Nightly","triggers":[{"name":"heartbeat","kind":"interval","interval":"1m","sandbox_policy":"new"}]}`,
		},
	}
	handler := NewProjectHandler(nil, store, nil)

	response, err := handler.GetScheduler(context.Background(), connect.NewRequest(&agentcomposev2.GetSchedulerRequest{
		Project:   &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: projectID}},
		AgentName: "reviewer",
	}))
	if err != nil {
		t.Fatalf("GetScheduler returned error: %v", err)
	}
	spec := response.Msg.GetSpec()
	if spec.GetSandboxPolicy() != agentcomposev2.SchedulerSandboxPolicy_SCHEDULER_SANDBOX_POLICY_STICKY ||
		spec.GetConcurrencyPolicy() != agentcomposev2.SchedulerConcurrencyPolicy_SCHEDULER_CONCURRENCY_POLICY_PARALLEL ||
		len(spec.GetTriggers()) != 1 ||
		spec.GetTriggers()[0].GetSandboxPolicy() != agentcomposev2.SchedulerSandboxPolicy_SCHEDULER_SANDBOX_POLICY_NEW {
		t.Fatalf("mapped scheduler spec = %#v", spec)
	}
}

type schedulerRuntimeProjectStore struct {
	project   domain.ProjectRecord
	scheduler domain.ProjectSchedulerRecord
}

func (s schedulerRuntimeProjectStore) GetProject(context.Context, string) (domain.ProjectRecord, error) {
	return s.project, nil
}

func (s schedulerRuntimeProjectStore) ListProjects(context.Context, domain.ProjectListOptions) (domain.ProjectListResult, error) {
	return domain.ProjectListResult{Projects: []domain.ProjectRecord{s.project}}, nil
}

func (s schedulerRuntimeProjectStore) ListProjectAgents(context.Context, string) ([]domain.ProjectAgentRecord, error) {
	return nil, nil
}

func (s schedulerRuntimeProjectStore) ListProjectSchedulers(context.Context, string) ([]domain.ProjectSchedulerRecord, error) {
	return []domain.ProjectSchedulerRecord{s.scheduler}, nil
}

func (s schedulerRuntimeProjectStore) GetProjectRevision(context.Context, string, int64) (domain.ProjectRevisionRecord, error) {
	return domain.ProjectRevisionRecord{}, nil
}

type schedulerRuntimeFake struct {
	scheduler    domain.Scheduler
	schedulerID  string
	triggerID    string
	enabledCalls int
	triggerCalls int
}

func (f *schedulerRuntimeFake) SetSchedulerEnabled(_ context.Context, schedulerID string, enabled bool) (domain.Scheduler, error) {
	f.enabledCalls++
	f.schedulerID = schedulerID
	f.scheduler.Summary.Enabled = enabled
	return f.scheduler, nil
}

func (f *schedulerRuntimeFake) SetSchedulerTriggerEnabled(_ context.Context, schedulerID, triggerID string, enabled bool) (domain.Scheduler, error) {
	f.triggerCalls++
	f.schedulerID = schedulerID
	f.triggerID = triggerID
	f.scheduler.Triggers[0].Enabled = enabled
	return f.scheduler, nil
}

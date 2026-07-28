package api

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestProjectHandlerSchedulerUpdatesUseLoaderRuntime(t *testing.T) {
	const (
		projectID = "project-1"
		agentName = "中文智能体"
		loaderID  = "loader-1"
		triggerID = "中文心跳"
	)
	store := schedulerRuntimeProjectStore{
		project: domain.ProjectRecord{ID: projectID, Name: "migration"},
		scheduler: domain.ProjectSchedulerRecord{
			ProjectID:   projectID,
			AgentName:   agentName,
			SchedulerID: "scheduler-1",
			ID:          loaderID,
		},
	}
	runtime := &schedulerRuntimeFake{loader: domain.Scheduler{
		Summary:  domain.SchedulerSummary{ID: loaderID},
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
	if runtime.enabledCalls != 1 || runtime.loaderID != loaderID || !enabled.Msg.GetScheduler().GetEnabled() {
		t.Fatalf("scheduler update calls=%d loader=%q response=%#v", runtime.enabledCalls, runtime.loaderID, enabled.Msg.GetScheduler())
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
	loader       domain.Scheduler
	loaderID     string
	triggerID    string
	enabledCalls int
	triggerCalls int
}

func (f *schedulerRuntimeFake) SetSchedulerEnabled(_ context.Context, loaderID string, enabled bool) (domain.Scheduler, error) {
	f.enabledCalls++
	f.loaderID = loaderID
	f.loader.Summary.Enabled = enabled
	return f.loader, nil
}

func (f *schedulerRuntimeFake) SetSchedulerTriggerEnabled(_ context.Context, loaderID, triggerID string, enabled bool) (domain.Scheduler, error) {
	f.triggerCalls++
	f.loaderID = loaderID
	f.triggerID = triggerID
	f.loader.Triggers[0].Enabled = enabled
	return f.loader, nil
}

package projects

import (
	"context"
	"testing"

	domain "agent-compose/pkg/model"
)

func TestReconcileSchedulersSkipsWritesForUnchangedScheduler(t *testing.T) {
	t.Parallel()

	project := domain.ProjectRecord{ID: "project-1"}
	trigger := domain.SchedulerTrigger{
		SchedulerID: "loader-1",
		ID:          "daily",
		Kind:        domain.SchedulerTriggerKindInterval,
		IntervalMs:  86_400_000,
		Enabled:     true,
	}
	scheduler := domain.ProjectSchedulerRecord{
		ProjectID:    project.ID,
		SchedulerID:  "scheduler-1",
		AgentName:    "worker",
		ID:           "loader-1",
		Revision:     3,
		Enabled:      true,
		TriggerCount: 1,
		SpecJSON:     `{"enabled":true}`,
	}
	loader := domain.Scheduler{
		Summary: domain.SchedulerSummary{
			ID: "loader-1", Name: "worker scheduler", Enabled: true, Runtime: domain.SchedulerRuntimeScheduler,
			DefaultAgent: "codex", SandboxPolicy: domain.SchedulerSandboxPolicySticky,
			ProjectID: project.ID, ProjectRevision: 3, AgentName: "worker", ProjectSchedulerID: scheduler.SchedulerID,
		},
		Script:   `scheduler.interval("daily", function daily() {}, 86400000);`,
		Triggers: []domain.SchedulerTrigger{trigger},
	}
	store := &unchangedSchedulerReconcileStore{scheduler: scheduler, loader: loader}

	changes, unchanged, err := ReconcileSchedulers(context.Background(), store, project, []domain.ProjectSchedulerRecord{scheduler}, []domain.Scheduler{loader}, ReconcileSchedulerOptions{})
	if err != nil {
		t.Fatalf("ReconcileSchedulers returned error: %v", err)
	}
	if !unchanged {
		t.Fatal("ReconcileSchedulers reported an identical scheduler as changed")
	}
	if len(changes) != 2 || changes[0].Action != ChangeActionUnchanged || changes[0].ResourceType != "project_scheduler" || changes[1].Action != ChangeActionUnchanged || changes[1].ResourceType != "loader" {
		t.Fatalf("changes = %#v", changes)
	}
	if len(store.writes) != 0 {
		t.Fatalf("identical scheduler caused writes: %v", store.writes)
	}
}

type unchangedSchedulerReconcileStore struct {
	scheduler domain.ProjectSchedulerRecord
	loader    domain.Scheduler
	writes    []string
}

func (s *unchangedSchedulerReconcileStore) GetProjectScheduler(context.Context, string, string) (domain.ProjectSchedulerRecord, error) {
	return s.scheduler, nil
}

func (s *unchangedSchedulerReconcileStore) UpsertProjectScheduler(_ context.Context, item domain.ProjectSchedulerRecord) (domain.ProjectSchedulerRecord, error) {
	s.writes = append(s.writes, "upsert scheduler")
	return item, nil
}

func (s *unchangedSchedulerReconcileStore) SetProjectSchedulerEnabled(_ context.Context, _, _ string, _ bool) (domain.ProjectSchedulerRecord, error) {
	s.writes = append(s.writes, "set scheduler enabled")
	return s.scheduler, nil
}

func (s *unchangedSchedulerReconcileStore) ListProjectSchedulers(context.Context, string) ([]domain.ProjectSchedulerRecord, error) {
	return []domain.ProjectSchedulerRecord{s.scheduler}, nil
}

func (s *unchangedSchedulerReconcileStore) GetScheduler(context.Context, string) (domain.Scheduler, error) {
	return s.loader, nil
}

func (s *unchangedSchedulerReconcileStore) ReplaceSchedulerTriggers(_ context.Context, _ string, triggers []domain.SchedulerTrigger) ([]domain.SchedulerTrigger, error) {
	s.writes = append(s.writes, "replace triggers")
	return triggers, nil
}

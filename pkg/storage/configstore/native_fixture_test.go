package configstore

import (
	"context"
	"fmt"
	"strings"

	"agent-compose/pkg/compose"
	domain "agent-compose/pkg/model"
	"agent-compose/internal/projects"
)

func upsertNativeTestScheduler(ctx context.Context, store *ConfigStore, scheduler domain.Scheduler) (domain.Scheduler, error) {
	projectID := strings.TrimSpace(scheduler.Summary.ProjectID)
	if projectID == "" {
		projectID = "test-project-" + scheduler.Summary.ID
	}
	agentName := strings.TrimSpace(scheduler.Summary.AgentName)
	if agentName == "" {
		agentName = "worker"
	}
	schedulerName := strings.TrimSpace(scheduler.Summary.ProjectSchedulerID)
	if schedulerName == "" {
		schedulerName = scheduler.Summary.ID
	}

	agent := compose.NormalizedAgentSpec{
		Name:        agentName,
		Enabled:     true,
		DisplayName: scheduler.Summary.Name,
		Description: scheduler.Summary.Description,
		Provider:    scheduler.Summary.DefaultAgent,
		Image:       scheduler.Summary.GuestImage,
		CapsetIDs:   append([]string(nil), scheduler.Summary.CapsetIDs...),
		Env:         testEnvSpec(scheduler.EnvItems),
		Scheduler: &compose.NormalizedSchedulerSpec{
			Enabled:           scheduler.Summary.Enabled,
			SandboxPolicy:     scheduler.Summary.SandboxPolicy,
			ConcurrencyPolicy: scheduler.Summary.ConcurrencyPolicy,
			DisplayName:       scheduler.Summary.Name,
			Description:       scheduler.Summary.Description,
			Script:            scheduler.Script,
		},
	}
	if workspaceID := strings.TrimSpace(scheduler.Summary.WorkspaceID); workspaceID != "" {
		agent.Workspace = &compose.WorkspaceSpec{Name: workspaceID}
	}
	if driver := strings.TrimSpace(scheduler.Summary.Driver); driver != "" {
		agent.Driver = &compose.NormalizedDriverSpec{Name: driver}
	}
	projectName := "test-" + projectID
	specJSON, err := (&compose.NormalizedProjectSpec{Name: projectName, Agents: []compose.NormalizedAgentSpec{agent}}).MarshalCanonicalJSON(false)
	if err != nil {
		return domain.Scheduler{}, fmt.Errorf("marshal native scheduler fixture: %w", err)
	}
	project, err := store.UpsertProject(ctx, domain.ProjectRecord{ID: projectID, Name: projectName})
	if err != nil {
		return domain.Scheduler{}, err
	}
	revision, _, err := store.SaveProjectRevision(ctx, domain.ProjectRevisionRecord{ProjectID: projectID, SpecHash: "fixture-" + scheduler.Summary.ID, SpecJSON: string(specJSON)})
	if err != nil {
		return domain.Scheduler{}, err
	}
	agentID := scheduler.Summary.AgentID
	if agentID == "" {
		agentID, err = projects.StableProjectAgentID(projectID, agentName)
		if err != nil {
			return domain.Scheduler{}, err
		}
	}
	if _, err := store.UpsertProjectAgent(ctx, domain.ProjectAgentRecord{ID: agentID, ProjectID: project.ID, AgentName: agentName, Revision: revision.Revision, SchedulerEnabled: true, SpecJSON: "{}"}); err != nil {
		return domain.Scheduler{}, err
	}
	record, err := store.UpsertProjectScheduler(ctx, domain.ProjectSchedulerRecord{
		ID: scheduler.Summary.ID, ProjectID: project.ID, SchedulerID: schedulerName, AgentName: agentName,
		Revision: revision.Revision, Enabled: scheduler.Summary.Enabled, TriggerCount: len(scheduler.Triggers), SpecJSON: "{}",
	})
	if err != nil {
		return domain.Scheduler{}, err
	}
	if _, err := store.ReplaceSchedulerTriggers(ctx, record.ID, scheduler.Triggers); err != nil {
		return domain.Scheduler{}, err
	}
	return store.GetScheduler(ctx, record.ID)
}

func testEnvSpec(items []domain.SandboxEnvVar) map[string]compose.EnvVarSpec {
	if len(items) == 0 {
		return nil
	}
	result := make(map[string]compose.EnvVarSpec, len(items))
	for _, item := range items {
		result[item.Name] = compose.EnvVarSpec{Value: item.Value, Secret: item.Secret}
	}
	return result
}

package runs

import (
	"context"
	"fmt"
	"strings"

	domain "agent-compose/pkg/model"
)

type SandboxRunTarget struct {
	ProjectID string
	AgentName string
}

type SandboxRunTargetStore interface {
	ListLatestProjectRunsForSandboxes(context.Context, []string) (map[string]domain.ProjectRunRecord, error)
	ListProjectAgentsByManagedAgentIDs(context.Context, []string) (map[string]domain.ProjectAgentRecord, error)
}

type SandboxRunTargetResolver struct {
	store SandboxRunTargetStore
}

func NewSandboxRunTargetResolver(store SandboxRunTargetStore) (*SandboxRunTargetResolver, error) {
	if store == nil {
		return nil, fmt.Errorf("sandbox run target store is required")
	}
	return &SandboxRunTargetResolver{store: store}, nil
}

func (r *SandboxRunTargetResolver) Resolve(ctx context.Context, sandbox *domain.Sandbox) (SandboxRunTarget, error) {
	resolved, err := r.ResolveBatch(ctx, []*domain.Sandbox{sandbox})
	if err != nil || sandbox == nil {
		return SandboxRunTarget{}, err
	}
	return resolved[sandbox.Summary.ID], nil
}

func (r *SandboxRunTargetResolver) ResolveBatch(ctx context.Context, sandboxes []*domain.Sandbox) (map[string]SandboxRunTarget, error) {
	result := make(map[string]SandboxRunTarget, len(sandboxes))
	sandboxIDs := make([]string, 0, len(sandboxes))
	managedAgentIDs := make([]string, 0, len(sandboxes))
	for _, sandbox := range sandboxes {
		if sandbox == nil {
			continue
		}
		sandboxIDs = append(sandboxIDs, sandbox.Summary.ID)
		if id := sandboxTagValue(sandbox, domain.AgentSandboxTagID); id != "" {
			managedAgentIDs = append(managedAgentIDs, id)
		}
	}
	runsBySandbox, err := r.store.ListLatestProjectRunsForSandboxes(ctx, sandboxIDs)
	if err != nil {
		return nil, err
	}
	agentsByManagedID, err := r.store.ListProjectAgentsByManagedAgentIDs(ctx, managedAgentIDs)
	if err != nil {
		return nil, err
	}
	for _, sandbox := range sandboxes {
		if sandbox == nil {
			continue
		}
		id := sandbox.Summary.ID
		if run, ok := runsBySandbox[id]; ok {
			result[id] = SandboxRunTarget{ProjectID: run.ProjectID, AgentName: run.AgentName}
			continue
		}
		projectID := sandboxTagValue(sandbox, "project")
		if projectID == "" {
			projectID = sandboxTagValue(sandbox, "project_id")
		}
		if agentName := sandboxTagValue(sandbox, "agent"); projectID != "" && agentName != "" {
			result[id] = SandboxRunTarget{ProjectID: projectID, AgentName: agentName}
			continue
		}
		if agent, ok := agentsByManagedID[sandboxTagValue(sandbox, domain.AgentSandboxTagID)]; ok {
			result[id] = SandboxRunTarget{ProjectID: agent.ProjectID, AgentName: agent.AgentName}
			continue
		}
	}
	return result, nil
}

func sandboxTagValue(sandbox *domain.Sandbox, name string) string {
	if sandbox == nil {
		return ""
	}
	for _, tag := range sandbox.Summary.Tags {
		if strings.TrimSpace(tag.Name) == name {
			return strings.TrimSpace(tag.Value)
		}
	}
	return ""
}

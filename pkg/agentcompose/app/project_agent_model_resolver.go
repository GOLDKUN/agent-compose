package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent-compose/pkg/compose"
	appconfig "agent-compose/pkg/config"
	"agent-compose/pkg/llms"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/projects"
	"agent-compose/pkg/storage/configstore"
)

type projectAgentModelResolver struct {
	config *appconfig.Config
	store  *configstore.ConfigStore
}

func newProjectAgentModelResolver(config *appconfig.Config, store *configstore.ConfigStore) *projectAgentModelResolver {
	return &projectAgentModelResolver{config: config, store: store}
}

// ResolveProjectAgentModels previews the model each current agent would select.
// It derives the minimal agent definition (provider, model, env) from the
// already-loaded project agent records instead of re-loading and re-parsing the
// whole project spec.
func (r *projectAgentModelResolver) ResolveProjectAgentModels(ctx context.Context, project domain.ProjectRecord, agents []domain.ProjectAgentRecord) (map[string]llms.AgentModelResolution, error) {
	if r == nil || r.store == nil || project.CurrentRevision <= 0 {
		return nil, nil
	}
	definitions := make([]domain.AgentDefinition, 0, len(agents))
	for _, agent := range agents {
		if agent.Revision != project.CurrentRevision {
			continue
		}
		definition, err := agentDefinitionForModelResolution(agent)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	resolved, err := llms.ResolveAgentModels(ctx, r.config, r.store, definitions)
	if err != nil {
		return nil, fmt.Errorf("resolve project agent models: %w", err)
	}
	resolutions := make(map[string]llms.AgentModelResolution, len(definitions))
	for i, definition := range definitions {
		resolutions[definition.AgentName] = resolved[i]
	}
	return resolutions, nil
}

// agentDefinitionForModelResolution builds the subset of an agent definition
// that model resolution needs. Provider and model come from the agent record;
// env items are decoded from the agent's own canonical spec JSON.
func agentDefinitionForModelResolution(record domain.ProjectAgentRecord) (domain.AgentDefinition, error) {
	raw := strings.TrimSpace(record.SpecJSON)
	if raw == "" {
		raw = "{}"
	}
	var spec compose.NormalizedAgentSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return domain.AgentDefinition{}, fmt.Errorf("decode project agent %s spec: %w", record.AgentName, err)
	}
	return domain.AgentDefinition{
		AgentName: record.AgentName,
		Provider:  record.Provider,
		Model:     record.Model,
		EnvItems:  projects.SandboxEnvItemsFromCompose(spec.Env),
	}, nil
}

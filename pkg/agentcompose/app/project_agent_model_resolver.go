package app

import (
	"context"
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

func (r *projectAgentModelResolver) ResolveProjectAgentModels(ctx context.Context, project domain.ProjectRecord) (map[string]llms.AgentModelResolution, error) {
	if r == nil || r.store == nil || project.CurrentRevision <= 0 {
		return nil, nil
	}
	revision, err := r.store.GetProjectRevision(ctx, project.ID, project.CurrentRevision)
	if err != nil {
		return nil, fmt.Errorf("get project revision %s/%d: %w", project.ID, project.CurrentRevision, err)
	}
	spec, err := compose.ParseCanonicalJSON([]byte(strings.TrimSpace(revision.SpecJSON)))
	if err != nil {
		return nil, fmt.Errorf("decode project revision %s/%d: %w", project.ID, project.CurrentRevision, err)
	}
	definitions, err := projects.NewAgentDefinitionsFromSpec(project, project.CurrentRevision, spec)
	if err != nil {
		return nil, fmt.Errorf("derive project agent definitions: %w", err)
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

package projects

import (
	"fmt"
	"strings"

	"agent-compose/pkg/compose"
	domain "agent-compose/pkg/model"
)

// AgentDefinitionFromRevision derives runtime agent configuration from an
// immutable project revision. Mutable project-agent rows are read models and
// must not override the revision selected by a run.
func AgentDefinitionFromRevision(project domain.ProjectRecord, revision domain.ProjectRevisionRecord, agentName string) (domain.AgentDefinition, error) {
	spec, err := compose.ParseCanonicalJSON([]byte(strings.TrimSpace(revision.SpecJSON)))
	if err != nil {
		return domain.AgentDefinition{}, fmt.Errorf("decode project revision %s/%d: %w", project.ID, revision.Revision, err)
	}
	agentName = strings.TrimSpace(agentName)
	for _, agent := range spec.Agents {
		if agent.Name != agentName {
			continue
		}
		definition, err := NewAgentDefinitionFromSpec(project, revision.Revision, agent, spec.MCPServers, spec.OctoBusServers)
		if err != nil {
			return domain.AgentDefinition{}, fmt.Errorf("derive project revision agent %s: %w", agentName, err)
		}
		return definition, nil
	}
	return domain.AgentDefinition{}, fmt.Errorf("project revision %s/%d missing agent %s", project.ID, revision.Revision, agentName)
}

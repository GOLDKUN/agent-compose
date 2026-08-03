package projects

import (
	"fmt"

	"agent-compose/pkg/compose"

	"gopkg.in/yaml.v3"
)

const secretRedactionMarker = "********"

// RestoreProjectSecrets returns a caller-owned copy of submitted with
// user-facing redaction markers replaced from the current persisted spec.
func RestoreProjectSecrets(current *compose.NormalizedProjectSpec, submitted *compose.ProjectSpec) (*compose.ProjectSpec, []ValidationIssue, error) {
	if submitted == nil {
		return nil, []ValidationIssue{{Path: "spec", Message: "project spec is required"}}, nil
	}
	cloned, err := cloneProjectSpec(submitted)
	if err != nil {
		return nil, nil, err
	}
	if current == nil {
		return cloned, []ValidationIssue{{Path: "spec", Message: "current project spec is required"}}, nil
	}

	var issues []ValidationIssue
	restoreEnvSecrets("variables", current.Variables, cloned.Variables, &issues)
	restoreMCPSecrets("mcp_servers", current.MCPServers, cloned.MCPServers, &issues)
	restoreOctoBusSecrets(current.OctoBusServers, cloned.OctoBusServers, &issues)
	rejectWorkspaceMarkers("workspaces", cloned.Workspaces, &issues)

	currentAgents := make(map[string]compose.NormalizedAgentSpec, len(current.Agents))
	for _, agent := range current.Agents {
		currentAgents[agent.Name] = agent
	}
	for name, agent := range cloned.Agents {
		path := "agents." + name
		currentAgent, found := currentAgents[name]
		if !found {
			rejectAgentMarkers(path, agent, &issues)
			continue
		}
		restoreEnvSecrets(path+".env", currentAgent.Env, agent.Env, &issues)
		restoreAgentMCPSecrets(path+".mcp_servers", currentAgent.MCPServers, agent.MCPServers, &issues)
		rejectWorkspaceMarker(path+".workspace", agent.Workspace, &issues)
		rejectSkillMarkers(path+".skills", agent.Skills, &issues)
		cloned.Agents[name] = agent
	}
	return cloned, issues, nil
}

func cloneProjectSpec(spec *compose.ProjectSpec) (*compose.ProjectSpec, error) {
	data, err := yaml.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal project spec clone: %w", err)
	}
	cloned, err := compose.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse project spec clone: %w", err)
	}
	return cloned, nil
}

func restoreEnvSecrets(path string, current, submitted map[string]compose.EnvVarSpec, issues *[]ValidationIssue) {
	for name, value := range submitted {
		if value.Value != secretRedactionMarker {
			continue
		}
		itemPath := path + "." + name + ".value"
		if !value.Secret {
			*issues = append(*issues, ValidationIssue{Path: itemPath, Message: "redacted secret marker requires secret: true"})
			continue
		}
		existing, found := current[name]
		if !found || !existing.Secret {
			*issues = append(*issues, ValidationIssue{Path: itemPath, Message: "redacted secret marker has no existing secret to preserve"})
			continue
		}
		value.Value = existing.Value
		submitted[name] = value
	}
}

func restoreMCPSecrets(path string, current map[string]compose.NormalizedMCPServerSpec, submitted map[string]compose.MCPServerSpec, issues *[]ValidationIssue) {
	for name, server := range submitted {
		currentServer := current[name]
		restoreEnvSecrets(path+"."+name+".env", currentServer.Env, server.Env, issues)
		restoreEnvSecrets(path+"."+name+".headers", currentServer.Headers, server.Headers, issues)
		submitted[name] = server
	}
}

func restoreAgentMCPSecrets(path string, current map[string]compose.NormalizedMCPServerSpec, submitted compose.AgentMCPEntriesSpec, issues *[]ValidationIssue) {
	for index := range submitted {
		server := &submitted[index]
		name := server.Name
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		if name != "" {
			itemPath = path + "." + name
		}
		currentServer := current[name]
		restoreEnvSecrets(itemPath+".env", currentServer.Env, server.Env, issues)
		restoreEnvSecrets(itemPath+".headers", currentServer.Headers, server.Headers, issues)
	}
}

func restoreOctoBusSecrets(current map[string]compose.NormalizedOctoBusServerSpec, submitted map[string]compose.OctoBusServerSpec, issues *[]ValidationIssue) {
	for name, server := range submitted {
		if server.Token != secretRedactionMarker {
			continue
		}
		existing, found := current[name]
		if !found || existing.Token == "" {
			*issues = append(*issues, ValidationIssue{Path: "octobus_servers." + name + ".token", Message: "redacted secret marker has no existing token to preserve"})
			continue
		}
		server.Token = existing.Token
		submitted[name] = server
	}
}

func rejectAgentMarkers(path string, agent compose.AgentSpec, issues *[]ValidationIssue) {
	restoreEnvSecrets(path+".env", nil, agent.Env, issues)
	restoreAgentMCPSecrets(path+".mcp_servers", nil, agent.MCPServers, issues)
	rejectWorkspaceMarker(path+".workspace", agent.Workspace, issues)
	rejectSkillMarkers(path+".skills", agent.Skills, issues)
}

func rejectWorkspaceMarkers(path string, workspaces map[string]compose.WorkspaceSpec, issues *[]ValidationIssue) {
	for name, workspace := range workspaces {
		rejectWorkspaceMarker(path+"."+name, &workspace, issues)
	}
}

func rejectWorkspaceMarker(path string, workspace *compose.WorkspaceSpec, issues *[]ValidationIssue) {
	if workspace == nil {
		return
	}
	if workspace.Password == secretRedactionMarker {
		*issues = append(*issues, ValidationIssue{Path: path + ".password", Message: "redacted secret marker cannot be used as a credential"})
	}
	if workspace.Token == secretRedactionMarker {
		*issues = append(*issues, ValidationIssue{Path: path + ".token", Message: "redacted secret marker cannot be used as a credential"})
	}
}

func rejectSkillMarkers(path string, skills []compose.SkillSpec, issues *[]ValidationIssue) {
	for index, skill := range skills {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		if skill.Name != "" {
			itemPath = path + "." + skill.Name
		}
		if skill.Password == secretRedactionMarker {
			*issues = append(*issues, ValidationIssue{Path: itemPath + ".password", Message: "redacted secret marker cannot be used as a credential"})
		}
		if skill.Token == secretRedactionMarker {
			*issues = append(*issues, ValidationIssue{Path: itemPath + ".token", Message: "redacted secret marker cannot be used as a credential"})
		}
	}
}

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
	restoreWorkspaceMarkers("workspaces", current.Workspaces, cloned.Workspaces, &issues)

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
		restoreWorkspaceMarker(path+".workspace", currentAgent.Workspace, agent.Workspace, &issues)
		restoreSkillMarkers(path+".skills", currentAgent.Skills, agent.Skills, &issues)
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
	restoreWorkspaceMarker(path+".workspace", nil, agent.Workspace, issues)
	restoreSkillMarkers(path+".skills", nil, agent.Skills, issues)
}

func restoreWorkspaceMarkers(path string, current map[string]compose.WorkspaceSpec, submitted map[string]compose.WorkspaceSpec, issues *[]ValidationIssue) {
	for name, workspace := range submitted {
		existing, found := current[name]
		if !found {
			restoreWorkspaceMarker(path+"."+name, nil, &workspace, issues)
		} else {
			restoreWorkspaceMarker(path+"."+name, &existing, &workspace, issues)
		}
		submitted[name] = workspace
	}
}

func restoreWorkspaceMarker(path string, current, submitted *compose.WorkspaceSpec, issues *[]ValidationIssue) {
	if submitted == nil {
		return
	}
	currentUsername, currentPassword, currentToken := "", "", ""
	if current != nil {
		currentUsername = current.Username
		currentPassword = current.Password
		currentToken = current.Token
	}
	restoreSourceCredentialMarker(path+".username", currentUsername, &submitted.Username, issues)
	restoreSourceCredentialMarker(path+".password", currentPassword, &submitted.Password, issues)
	restoreSourceCredentialMarker(path+".token", currentToken, &submitted.Token, issues)
}

func restoreSkillMarkers(path string, current []compose.NormalizedSkillSpec, submitted []compose.SkillSpec, issues *[]ValidationIssue) {
	currentByName := make(map[string]compose.NormalizedSkillSpec, len(current))
	for _, skill := range current {
		currentByName[skill.Name] = skill
	}
	for index := range submitted {
		skill := &submitted[index]
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		if skill.Name != "" {
			itemPath = path + "." + skill.Name
		}
		existing := currentByName[skill.Name]
		restoreSourceCredentialMarker(itemPath+".username", existing.Username, &skill.Username, issues)
		restoreSourceCredentialMarker(itemPath+".password", existing.Password, &skill.Password, issues)
		restoreSourceCredentialMarker(itemPath+".token", existing.Token, &skill.Token, issues)
	}
}

func restoreSourceCredentialMarker(path, current string, submitted *string, issues *[]ValidationIssue) {
	if submitted == nil || *submitted != secretRedactionMarker {
		return
	}
	if current == "" {
		*issues = append(*issues, ValidationIssue{Path: path, Message: "redacted secret marker has no existing credential to preserve"})
		return
	}
	*submitted = current
}

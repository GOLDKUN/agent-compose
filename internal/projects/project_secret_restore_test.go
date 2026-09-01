package projects

import (
	"strings"
	"testing"

	"github.com/chaitin/agent-compose/pkg/compose"
)

func TestRestoreProjectSecretsPreservesStableRedactedValues(t *testing.T) {
	current := normalizedProjectForSecretRestoreTest(t, strings.Join([]string{
		"name: demo",
		"variables:",
		"  PROJECT_TOKEN: {value: project-secret, secret: true}",
		"mcp_servers:",
		"  project-tools:",
		"    type: remote",
		"    transport: http",
		"    url: https://example.test/mcp",
		"    headers:",
		"      Authorization: {value: project-header, secret: true}",
		"octobus_servers:",
		"  capabilities:",
		"    url: https://octobus.example.test",
		"    token: octobus-secret",
		"workspaces:",
		"  source:",
		"    provider: git",
		"    url: https://git.example/workspace.git",
		"    username: workspace-user",
		"    token: workspace-token",
		"agents:",
		"  reviewer:",
		"    provider: openai",
		"    model: old-model",
		"    env:",
		"      AGENT_TOKEN: {value: agent-secret, secret: true}",
		"    mcp_servers:",
		"      - name: agent-tools",
		"        type: local",
		"        command: tools",
		"        env:",
		"          MCP_TOKEN: {value: mcp-secret, secret: true}",
		"    skills:",
		"      - name: private-skill",
		"        provider: git",
		"        url: https://git.example/skill.git",
		"        username: skill-user",
		"        password: skill-password",
		"        token: skill-token",
	}, "\n"))
	submitted := projectSpecForSecretRestoreTest(t, strings.Join([]string{
		"name: demo",
		"variables:",
		"  PROJECT_TOKEN: {value: '********', secret: true}",
		"mcp_servers:",
		"  project-tools:",
		"    type: remote",
		"    transport: http",
		"    url: https://example.test/mcp",
		"    headers:",
		"      Authorization: {value: '********', secret: true}",
		"octobus_servers:",
		"  capabilities:",
		"    url: https://octobus.example.test",
		"    token: '********'",
		"workspaces:",
		"  source:",
		"    provider: git",
		"    url: https://git.example/workspace.git",
		"    username: '********'",
		"    token: '********'",
		"agents:",
		"  reviewer:",
		"    provider: openai",
		"    model: new-model",
		"    env:",
		"      AGENT_TOKEN: {value: '********', secret: true}",
		"    mcp_servers:",
		"      - name: agent-tools",
		"        type: local",
		"        command: tools",
		"        env:",
		"          MCP_TOKEN: {value: '********', secret: true}",
		"    skills:",
		"      - name: private-skill",
		"        provider: git",
		"        url: https://git.example/skill.git",
		"        username: '********'",
		"        password: '********'",
		"        token: '********'",
	}, "\n"))

	restored, issues, err := RestoreProjectSecrets(current, submitted)
	if err != nil || len(issues) != 0 {
		t.Fatalf("RestoreProjectSecrets() issues=%#v err=%v", issues, err)
	}
	if got := restored.Variables["PROJECT_TOKEN"].Value; got != "project-secret" {
		t.Fatalf("project token = %q", got)
	}
	if got := restored.MCPServers["project-tools"].Headers["Authorization"].Value; got != "project-header" {
		t.Fatalf("project MCP header = %q", got)
	}
	if got := restored.OctoBusServers["capabilities"].Token; got != "octobus-secret" {
		t.Fatalf("OctoBus token = %q", got)
	}
	if workspace := restored.Workspaces["source"]; workspace.Username != "workspace-user" || workspace.Token != "workspace-token" {
		t.Fatalf("workspace credentials = %#v", workspace)
	}
	agent := restored.Agents["reviewer"]
	if got := agent.Env["AGENT_TOKEN"].Value; got != "agent-secret" {
		t.Fatalf("agent token = %q", got)
	}
	if got := agent.MCPServers[0].Env["MCP_TOKEN"].Value; got != "mcp-secret" {
		t.Fatalf("agent MCP token = %q", got)
	}
	if skill := agent.Skills[0]; skill.Username != "skill-user" || skill.Password != "skill-password" || skill.Token != "skill-token" {
		t.Fatalf("skill credentials = %#v", skill)
	}
	if got := agent.Model; got != "new-model" {
		t.Fatalf("agent model = %q", got)
	}
	if got := submitted.Variables["PROJECT_TOKEN"].Value; got != secretRedactionMarker {
		t.Fatalf("RestoreProjectSecrets mutated submitted value = %q", got)
	}
}

func TestRestoreProjectSecretsRejectsUnmatchedAndNonSecretMarkers(t *testing.T) {
	current := normalizedProjectForSecretRestoreTest(t, strings.Join([]string{
		"name: demo",
		"variables:",
		"  EXISTING: {value: stored, secret: true}",
		"  PUBLIC: public",
		"agents:",
		"  reviewer:",
		"    provider: openai",
		"    model: test",
	}, "\n"))
	submitted := projectSpecForSecretRestoreTest(t, strings.Join([]string{
		"name: demo",
		"variables:",
		"  NEW_SECRET: {value: '********', secret: true}",
		"  PUBLIC: '********'",
		"agents:",
		"  reviewer:",
		"    provider: openai",
		"    model: test",
		"    skills:",
		"      - name: private-skill",
		"        provider: git",
		"        url: https://example.test/skill.git",
		"        token: '********'",
	}, "\n"))

	_, issues, err := RestoreProjectSecrets(current, submitted)
	if err != nil {
		t.Fatalf("RestoreProjectSecrets() error = %v", err)
	}
	if len(issues) != 3 {
		t.Fatalf("issues = %#v, want 3", issues)
	}
	var paths strings.Builder
	for _, issue := range issues {
		paths.WriteString(issue.Path)
		paths.WriteByte('\n')
	}
	for _, path := range []string{"variables.NEW_SECRET.value", "variables.PUBLIC.value", "agents.reviewer.skills.private-skill.token"} {
		if !strings.Contains(paths.String(), path) {
			t.Fatalf("issue paths %q do not contain %q", paths.String(), path)
		}
	}
}

func TestRestoreProjectSecretsUsesRealValuesAndDeletesOmittedItems(t *testing.T) {
	current := normalizedProjectForSecretRestoreTest(t, strings.Join([]string{
		"name: demo",
		"variables:",
		"  REPLACED: {value: old-secret, secret: true}",
		"  DELETED: {value: deleted-secret, secret: true}",
	}, "\n"))
	submitted := projectSpecForSecretRestoreTest(t, strings.Join([]string{
		"name: demo",
		"variables:",
		"  REPLACED: {value: new-secret, secret: true}",
	}, "\n"))

	restored, issues, err := RestoreProjectSecrets(current, submitted)
	if err != nil || len(issues) != 0 {
		t.Fatalf("RestoreProjectSecrets() issues=%#v err=%v", issues, err)
	}
	if got := restored.Variables["REPLACED"].Value; got != "new-secret" {
		t.Fatalf("replacement secret = %q", got)
	}
	if _, found := restored.Variables["DELETED"]; found {
		t.Fatalf("omitted secret was restored: %#v", restored.Variables)
	}
}

func projectSpecForSecretRestoreTest(t *testing.T, data string) *compose.ProjectSpec {
	t.Helper()
	spec, err := compose.Parse([]byte(data))
	if err != nil {
		t.Fatalf("compose.Parse() error = %v", err)
	}
	return spec
}

func normalizedProjectForSecretRestoreTest(t *testing.T, data string) *compose.NormalizedProjectSpec {
	t.Helper()
	spec := projectSpecForSecretRestoreTest(t, data)
	normalized, err := compose.Normalize(spec, compose.NormalizeOptions{SourceCredentials: compose.SourceCredentialsResolved})
	if err != nil {
		t.Fatalf("compose.Normalize() error = %v", err)
	}
	return normalized
}

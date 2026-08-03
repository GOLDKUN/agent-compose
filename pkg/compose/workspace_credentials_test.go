package compose

import (
	"bytes"
	"strings"
	"testing"
)

func TestNormalizeResolvesGitWorkspaceCredentials(t *testing.T) {
	spec := mustParseCompose(t, `
name: private-workspaces
workspaces:
  shared:
    provider: git
    url: https://example.test/shared.git
    username: ${GIT_USER}
    password: ${GIT_PASSWORD}
agents:
  worker:
    workspace:
      provider: git
      url: https://example.test/agent.git
      token: ${GIT_TOKEN}
`)

	normalized, err := Normalize(spec, NormalizeOptions{Env: map[string]string{
		"GIT_USER":     "git-user",
		"GIT_PASSWORD": "git-password",
		"GIT_TOKEN":    "git-token",
	}})
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if workspace := normalized.Workspaces["shared"]; workspace.Username != "git-user" || workspace.Password != "git-password" {
		t.Fatalf("shared workspace credentials = %#v", workspace)
	}
	if workspace := normalized.Agents[0].Workspace; workspace == nil || workspace.Token != "git-token" {
		t.Fatalf("agent workspace = %#v", workspace)
	}

	redacted, err := normalized.MarshalCanonicalJSON(true)
	if err != nil {
		t.Fatalf("MarshalCanonicalJSON returned error: %v", err)
	}
	for _, secret := range []string{"git-user", "git-password", "git-token"} {
		if bytes.Contains(redacted, []byte(secret)) {
			t.Fatalf("redacted output leaked workspace credential %q: %s", secret, redacted)
		}
	}
	if got := bytes.Count(redacted, []byte(redactedWorkspaceCredential)); got != 3 {
		t.Fatalf("redacted marker count = %d, want 3: %s", got, redacted)
	}
}

func TestNormalizeGitWorkspaceRequiresCredentialEnvironment(t *testing.T) {
	spec := mustParseCompose(t, `
name: private-workspace
workspaces:
  shared:
    provider: git
    url: https://example.test/shared.git
    token: ${GIT_TOKEN}
agents: {}
`)

	_, err := Normalize(spec, NormalizeOptions{Env: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "workspaces.shared.token") || !strings.Contains(err.Error(), "GIT_TOKEN") {
		t.Fatalf("Normalize error = %v, want missing workspace token environment error", err)
	}
}

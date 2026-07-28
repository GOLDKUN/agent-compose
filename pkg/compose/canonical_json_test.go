package compose

import (
	"bytes"
	"testing"
)

func TestParseCanonicalJSONRoundTrip(t *testing.T) {
	normalized := mustNormalizeCompose(t, `
name: canonical
variables:
  PROJECT_TOKEN:
    value: secret
    secret: true
workspaces:
  source:
    provider: git
    url: https://example.test/repo.git
mcp_servers:
  tools:
    type: local
    command: tool-server
    env:
      MODE: test
volumes:
  cache:
    driver: local
agents:
  worker:
    provider: codex
    env:
      AGENT_VALUE: agent
    volumes:
      - source: cache
        target: /cache
    workspace:
      provider: git
      url: https://example.test/repo.git
`, nil)

	data, err := normalized.MarshalCanonicalJSON(false)
	if err != nil {
		t.Fatalf("MarshalCanonicalJSON returned error: %v", err)
	}
	parsed, err := ParseCanonicalJSON(data)
	if err != nil {
		t.Fatalf("ParseCanonicalJSON returned error: %v", err)
	}
	roundTrip, err := parsed.MarshalCanonicalJSON(false)
	if err != nil {
		t.Fatalf("round-trip MarshalCanonicalJSON returned error: %v", err)
	}
	if !bytes.Equal(roundTrip, data) {
		t.Fatalf("round-trip canonical JSON differs:\n got: %s\nwant: %s", roundTrip, data)
	}
	if parsed.Variables["PROJECT_TOKEN"].Value != "secret" || parsed.Agents[0].Env["AGENT_VALUE"].Value != "agent" {
		t.Fatalf("parsed environment maps = %#v / %#v", parsed.Variables, parsed.Agents[0].Env)
	}
	if parsed.Workspaces["source"].Provider != "git" || parsed.MCPServers["tools"].Command != "tool-server" || parsed.Volumes["cache"].Driver != "local" {
		t.Fatalf("parsed project maps = %#v / %#v / %#v", parsed.Workspaces, parsed.MCPServers, parsed.Volumes)
	}
}

func TestParseCanonicalJSONDefaultsHistoricalAgentEnabled(t *testing.T) {
	parsed, err := ParseCanonicalJSON([]byte(`{"name":"historical","agents":[{"name":"missing"},{"name":"disabled","enabled":false}]}`))
	if err != nil {
		t.Fatalf("ParseCanonicalJSON returned error: %v", err)
	}
	if !parsed.Agents[0].Enabled {
		t.Fatal("historical agent without enabled field is disabled")
	}
	if parsed.Agents[1].Enabled {
		t.Fatal("explicitly disabled agent was enabled")
	}
}

func TestParseCanonicalJSONRejectsMalformedJSON(t *testing.T) {
	if _, err := ParseCanonicalJSON([]byte(`{"agents":`)); err == nil {
		t.Fatal("ParseCanonicalJSON accepted malformed JSON")
	}
}

package migrate

import (
	"strings"
	"testing"
)

func TestLegacyProjectOctoBusServersMergesIdenticalDefinitionsDeterministically(t *testing.T) {
	agents := []convertedStandaloneAgent{
		{definition: legacyAgentDefinition{id: "agent-b", configJSON: `{"octobus_servers":{"zeta":{"url":"https://zeta.example"},"shared":{"url":"https://shared.example","token":"secret"}}}`}},
		{definition: legacyAgentDefinition{id: "agent-a", configJSON: `{"octobus_servers":{"alpha":{"url":"https://alpha.example"},"shared":{"url":"https://shared.example","token":"secret"}}}`}},
	}
	servers, err := legacyProjectOctoBusServers(agents)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 3 || servers[0]["name"] != "alpha" || servers[1]["name"] != "shared" || servers[2]["name"] != "zeta" || servers[1]["token"] != "secret" {
		t.Fatalf("merged OctoBus servers=%#v", servers)
	}
}

func TestLegacyProjectOctoBusServersRejectsConflictingDefinitions(t *testing.T) {
	agents := []convertedStandaloneAgent{
		{definition: legacyAgentDefinition{id: "agent-a", configJSON: `{"octobus_servers":{"internal":{"url":"https://first.example"}}}`}},
		{definition: legacyAgentDefinition{id: "agent-b", configJSON: `{"octobus_servers":{"internal":{"url":"https://second.example"}}}`}},
	}
	_, err := legacyProjectOctoBusServers(agents)
	if err == nil || !strings.Contains(err.Error(), "agent-a and agent-b") || !strings.Contains(err.Error(), "internal") {
		t.Fatalf("conflicting OctoBus error=%v", err)
	}
}

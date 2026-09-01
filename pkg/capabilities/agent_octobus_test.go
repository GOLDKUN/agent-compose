package capabilities

import (
	"testing"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestAgentOctoBusServersRejectsMalformedConfig(t *testing.T) {
	_, err := AgentOctoBusServers(domain.AgentDefinition{ConfigJSON: "{"})
	if err == nil {
		t.Fatal("AgentOctoBusServers returned nil error for malformed config")
	}
}

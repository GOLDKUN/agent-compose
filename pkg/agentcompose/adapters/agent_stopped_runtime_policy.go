package adapters

import (
	"encoding/json"
	"github.com/chaitin/agent-compose/pkg/compose"
	domain "github.com/chaitin/agent-compose/pkg/model"
	"strings"
)

func stoppedRuntimePolicyFromAgentDefinition(definition *domain.AgentDefinition) string {
	if definition == nil {
		return domain.DefaultStoppedRuntimePolicy
	}
	var config struct {
		Sandbox *struct {
			StoppedRuntimePolicy string `json:"stopped_runtime_policy"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(definition.ConfigJSON)), &config); err != nil {
		return domain.StoppedRuntimePolicyRetain
	}
	if config.Sandbox == nil {
		return domain.DefaultStoppedRuntimePolicy
	}
	policy, err := compose.NormalizeStoppedRuntimePolicy(config.Sandbox.StoppedRuntimePolicy)
	if err != nil {
		return domain.StoppedRuntimePolicyRetain
	}
	return policy
}

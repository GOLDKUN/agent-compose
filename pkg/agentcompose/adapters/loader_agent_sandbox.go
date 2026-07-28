package adapters

import (
	"encoding/json"
	"strings"

	domain "agent-compose/pkg/model"
)

func stoppedRuntimePolicyFromAgentDefinition(definition *domain.AgentDefinition) string {
	if definition == nil {
		return domain.StoppedRuntimePolicyRetain
	}
	var config struct {
		Sandbox *struct {
			StoppedRuntimePolicy string `json:"stopped_runtime_policy"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(definition.ConfigJSON)), &config); err != nil || config.Sandbox == nil {
		return domain.StoppedRuntimePolicyRetain
	}
	policy, err := domain.NormalizeStoppedRuntimePolicy(config.Sandbox.StoppedRuntimePolicy)
	if err != nil {
		return domain.StoppedRuntimePolicyRetain
	}
	return policy
}

package runs

import (
	"encoding/json"
	"strings"

	"agent-compose/pkg/compose"
	domain "agent-compose/pkg/model"
)

// driverK8sOptionsFromAgentDefinition recovers the agent's `driver.k8s`
// override (cluster context, namespace) from AgentDefinition.ConfigJSON.
// AgentDefinition has no dedicated column for it, same as Jupyter/Sandbox/
// Workspace (see pkg/projects/records.go: agentDefinitionConfig) - it rides
// along in the same JSON blob and is decoded back out narrowly here, the
// same way stoppedRuntimePolicyFromAgentDefinition does for its own field.
func driverK8sOptionsFromAgentDefinition(definition *domain.AgentDefinition) *compose.K8sDriverSpec {
	if definition == nil {
		return nil
	}
	var config struct {
		DriverK8s *compose.K8sDriverSpec `json:"driver_k8s"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(definition.ConfigJSON)), &config); err != nil {
		return nil
	}
	return config.DriverK8s
}

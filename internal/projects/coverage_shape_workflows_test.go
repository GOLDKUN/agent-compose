package projects

import "testing"

func TestE2EProjectRecordMCPWorkflows(t *testing.T) {
	t.Run("agent definition mcp", TestNewAgentDefinitionFromSpecPreservesMCPConfig)
	t.Run("agent definition jupyter", TestNewAgentDefinitionFromSpecPreservesJupyterConfig)
	t.Run("project record volumes", TestProjectRecordsCarryVolumeMountSpecs)
}

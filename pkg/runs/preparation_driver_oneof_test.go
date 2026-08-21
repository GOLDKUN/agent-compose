package runs

import (
	"testing"

	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

// DecodeRevisionSpec must preserve oneof fields (e.g. driver runtime config).
// Regression test for the GetProject → PatchProject round-trip where a driver
// decoded via encoding/json lost its config oneof, so PatchProject rejected the
// returned spec with "driver requires exactly one runtime config".
func TestDecodeRevisionSpecPreservesDriverRuntimeConfig(t *testing.T) {
	spec, err := DecodeRevisionSpec(`{"name":"demo","agents":[{"name":"worker","driver":{"name":"docker","docker":{}}}]}`)
	if err != nil {
		t.Fatalf("DecodeRevisionSpec returned error: %v", err)
	}
	driver := spec.GetAgents()[0].GetDriver()
	if driver.GetName() != "docker" {
		t.Fatalf("driver name = %q, want docker", driver.GetName())
	}
	if _, ok := driver.GetConfig().(*agentcomposev2.DriverSpec_Docker); !ok {
		t.Fatalf("driver config = %T, want *agentcomposev2.DriverSpec_Docker", driver.GetConfig())
	}
}

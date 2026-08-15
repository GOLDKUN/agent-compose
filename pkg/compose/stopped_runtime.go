package compose

import (
	"fmt"
	"strings"

	domain "agent-compose/pkg/model"
)

// NormalizeStoppedRuntimePolicy validates the stopped-runtime policy declared in
// a compose file and defaults an unset value. Deciding what an existing sandbox
// should do is a runtime question and lives in pkg/sandboxes.
func NormalizeStoppedRuntimePolicy(value string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "":
		return domain.DefaultStoppedRuntimePolicy, nil
	case domain.StoppedRuntimePolicyRetain:
		return domain.StoppedRuntimePolicyRetain, nil
	case domain.StoppedRuntimePolicyRemove:
		return domain.StoppedRuntimePolicyRemove, nil
	default:
		return "", fmt.Errorf("stopped runtime policy must be %q or %q", domain.StoppedRuntimePolicyRetain, domain.StoppedRuntimePolicyRemove)
	}
}

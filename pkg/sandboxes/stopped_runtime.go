package sandboxes

import (
	"strings"

	"github.com/chaitin/agent-compose/pkg/compose"
	domain "github.com/chaitin/agent-compose/pkg/model"
)

// EffectiveStoppedRuntimePolicy reports the policy that applies to one sandbox.
func EffectiveStoppedRuntimePolicy(sandbox *domain.Sandbox) string {
	// Sandboxes created before the policy was introduced have no snapshot.
	// Preserve their retained runtime instead of applying today's default.
	if sandbox == nil || strings.TrimSpace(sandbox.StoppedRuntimePolicy) == "" {
		return domain.StoppedRuntimePolicyRetain
	}
	policy, err := compose.NormalizeStoppedRuntimePolicy(sandbox.StoppedRuntimePolicy)
	if err != nil {
		return domain.StoppedRuntimePolicyRetain
	}
	return policy
}

// EffectiveStoppedRuntimeState reports the release state of one sandbox, mapping
// the legacy nil snapshot to the retained state.
func EffectiveStoppedRuntimeState(sandbox *domain.Sandbox) string {
	if sandbox == nil || sandbox.StoppedRuntime == nil {
		return domain.StoppedRuntimeStateRetained
	}
	switch sandbox.StoppedRuntime.State {
	case domain.StoppedRuntimeStateReleasePending, domain.StoppedRuntimeStateReleased:
		return sandbox.StoppedRuntime.State
	default:
		return domain.StoppedRuntimeStateRetained
	}
}

// RuntimeReleaseIntentional distinguishes a deliberate runtime release from
// unexpected runtime loss.
func RuntimeReleaseIntentional(sandbox *domain.Sandbox) bool {
	state := EffectiveStoppedRuntimeState(sandbox)
	return state == domain.StoppedRuntimeStateReleasePending || state == domain.StoppedRuntimeStateReleased
}

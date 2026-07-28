package model

import (
	"fmt"
	"strings"
	"time"
)

const (
	StoppedRuntimePolicyRetain = "retain"
	StoppedRuntimePolicyRemove = "remove"
	// DefaultStoppedRuntimePolicy is snapshotted for newly created sandboxes.
	DefaultStoppedRuntimePolicy = StoppedRuntimePolicyRemove

	StoppedRuntimeStateRetained       = "retained"
	StoppedRuntimeStateReleasePending = "release_pending"
	StoppedRuntimeStateReleased       = "released"
)

// StoppedRuntime records an intentional runtime release without conflating it
// with unexpected runtime loss. A nil value is the legacy retained state.
type StoppedRuntime struct {
	State       string    `json:"state"`
	RequestedAt time.Time `json:"requested_at,omitempty"`
	ReleasedAt  time.Time `json:"released_at,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
}

func NormalizeStoppedRuntimePolicy(value string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "":
		return DefaultStoppedRuntimePolicy, nil
	case StoppedRuntimePolicyRetain:
		return StoppedRuntimePolicyRetain, nil
	case StoppedRuntimePolicyRemove:
		return StoppedRuntimePolicyRemove, nil
	default:
		return "", fmt.Errorf("stopped runtime policy must be %q or %q", StoppedRuntimePolicyRetain, StoppedRuntimePolicyRemove)
	}
}

func EffectiveStoppedRuntimePolicy(sandbox *Sandbox) string {
	// Sandboxes created before the policy was introduced have no snapshot.
	// Preserve their retained runtime instead of applying today's default.
	if sandbox == nil || strings.TrimSpace(sandbox.StoppedRuntimePolicy) == "" {
		return StoppedRuntimePolicyRetain
	}
	policy, err := NormalizeStoppedRuntimePolicy(sandbox.StoppedRuntimePolicy)
	if err != nil {
		return StoppedRuntimePolicyRetain
	}
	return policy
}

func EffectiveStoppedRuntimeState(sandbox *Sandbox) string {
	if sandbox == nil || sandbox.StoppedRuntime == nil {
		return StoppedRuntimeStateRetained
	}
	switch sandbox.StoppedRuntime.State {
	case StoppedRuntimeStateReleasePending, StoppedRuntimeStateReleased:
		return sandbox.StoppedRuntime.State
	default:
		return StoppedRuntimeStateRetained
	}
}

func SandboxRuntimeReleaseIntentional(sandbox *Sandbox) bool {
	state := EffectiveStoppedRuntimeState(sandbox)
	return state == StoppedRuntimeStateReleasePending || state == StoppedRuntimeStateReleased
}

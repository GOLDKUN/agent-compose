package model

import (
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

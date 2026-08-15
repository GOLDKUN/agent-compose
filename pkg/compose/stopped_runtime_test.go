package compose

import (
	"testing"

	domain "agent-compose/pkg/model"
)

func TestNormalizeStoppedRuntimePolicy(t *testing.T) {
	tests := []struct {
		value string
		want  string
		ok    bool
	}{
		{want: domain.StoppedRuntimePolicyRemove, ok: true},
		{value: " retain ", want: domain.StoppedRuntimePolicyRetain, ok: true},
		{value: "REMOVE", want: domain.StoppedRuntimePolicyRemove, ok: true},
		{value: "prune"},
	}
	for _, tt := range tests {
		got, err := NormalizeStoppedRuntimePolicy(tt.value)
		if (err == nil) != tt.ok || got != tt.want {
			t.Fatalf("NormalizeStoppedRuntimePolicy(%q) = %q, %v; want %q, ok=%v", tt.value, got, err, tt.want, tt.ok)
		}
	}
}

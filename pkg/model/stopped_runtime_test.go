package model

import "testing"

func TestNormalizeStoppedRuntimePolicy(t *testing.T) {
	tests := []struct {
		value string
		want  string
		ok    bool
	}{
		{want: StoppedRuntimePolicyRemove, ok: true},
		{value: " retain ", want: StoppedRuntimePolicyRetain, ok: true},
		{value: "REMOVE", want: StoppedRuntimePolicyRemove, ok: true},
		{value: "prune"},
	}
	for _, tt := range tests {
		got, err := NormalizeStoppedRuntimePolicy(tt.value)
		if (err == nil) != tt.ok || got != tt.want {
			t.Fatalf("NormalizeStoppedRuntimePolicy(%q) = %q, %v; want %q, ok=%v", tt.value, got, err, tt.want, tt.ok)
		}
	}
}

func TestLegacySandboxDefaultsToRetainedRuntime(t *testing.T) {
	for _, sandbox := range []*Sandbox{nil, {}, {StoppedRuntimePolicy: " "}, {StoppedRuntimePolicy: "invalid"}} {
		if policy := EffectiveStoppedRuntimePolicy(sandbox); policy != StoppedRuntimePolicyRetain {
			t.Fatalf("legacy policy = %q, want retain", policy)
		}
	}
	sandbox := &Sandbox{}
	if state := EffectiveStoppedRuntimeState(sandbox); state != StoppedRuntimeStateRetained {
		t.Fatalf("legacy state = %q, want retained", state)
	}
}

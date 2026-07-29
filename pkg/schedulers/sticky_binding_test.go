package schedulers

import (
	"testing"
	"time"

	domain "agent-compose/pkg/model"
)

func TestSchedulerBindingsMatchComparesStickyState(t *testing.T) {
	current := domain.SchedulerBinding{
		SchedulerID:       "scheduler-1",
		TriggerID:         "trigger-1",
		SandboxID:         "sandbox-1",
		SandboxConfigHash: "sha256:current",
		CreatedAt:         time.Unix(1, 0),
		UpdatedAt:         time.Unix(2, 0),
	}
	expected := current
	expected.SchedulerID = " scheduler-1 "
	expected.TriggerID = " trigger-1 "
	expected.SandboxID = " sandbox-1 "
	expected.SandboxConfigHash = " sha256:current "
	expected.CreatedAt = time.Unix(3, 0)
	expected.UpdatedAt = time.Unix(4, 0)
	if !SchedulerBindingsMatch(current, expected) {
		t.Fatal("equivalent sticky binding state did not match")
	}

	for name, mutate := range map[string]func(*domain.SchedulerBinding){
		"scheduler":   func(binding *domain.SchedulerBinding) { binding.SchedulerID = "scheduler-2" },
		"trigger":     func(binding *domain.SchedulerBinding) { binding.TriggerID = "trigger-2" },
		"sandbox":     func(binding *domain.SchedulerBinding) { binding.SandboxID = "sandbox-2" },
		"config hash": func(binding *domain.SchedulerBinding) { binding.SandboxConfigHash = "sha256:other" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := current
			mutate(&changed)
			if SchedulerBindingsMatch(current, changed) {
				t.Fatalf("binding with changed %s matched", name)
			}
		})
	}
}

func TestRetiringSchedulerBindingPreservesSandboxAndTracksDesiredConfig(t *testing.T) {
	binding := domain.SchedulerBinding{
		SchedulerID:       "scheduler-1",
		TriggerID:         "trigger-1",
		SandboxID:         "sandbox-1",
		SandboxConfigHash: "sha256:old",
	}
	retiring := RetiringSchedulerBinding(binding, " sha256:new ")
	if retiring.SchedulerID != binding.SchedulerID || retiring.TriggerID != binding.TriggerID || retiring.SandboxID != binding.SandboxID {
		t.Fatalf("retiring binding changed identity: got %#v want %#v", retiring, binding)
	}
	if desiredHash, ok := RetiringSchedulerBindingConfigHash(retiring); !ok || desiredHash != "sha256:new" {
		t.Fatalf("RetiringSchedulerBindingConfigHash = %q/%v, want sha256:new/true", desiredHash, ok)
	}
	if _, ok := RetiringSchedulerBindingConfigHash(binding); ok {
		t.Fatal("ordinary binding reported as retiring")
	}
}

func TestAdoptLegacySchedulerBindingConfigHash(t *testing.T) {
	binding := domain.SchedulerBinding{
		SchedulerID: "scheduler-1",
		TriggerID:   "trigger-1",
		SandboxID:   "sandbox-1",
	}
	adopted, ok := AdoptLegacySchedulerBindingConfigHash(binding, " sha256:current ")
	if !ok {
		t.Fatal("legacy binding was not adopted")
	}
	if adopted.SchedulerID != binding.SchedulerID || adopted.TriggerID != binding.TriggerID || adopted.SandboxID != binding.SandboxID {
		t.Fatalf("adopted binding changed identity: got %#v want %#v", adopted, binding)
	}
	if adopted.SandboxConfigHash != "sha256:current" {
		t.Fatalf("adopted config hash = %q, want sha256:current", adopted.SandboxConfigHash)
	}
	if binding.SandboxConfigHash != "" {
		t.Fatalf("AdoptLegacySchedulerBindingConfigHash mutated its input: %#v", binding)
	}

	for name, test := range map[string]struct {
		binding domain.SchedulerBinding
		desired string
	}{
		"current binding": {binding: adopted, desired: "sha256:other"},
		"empty desired":   {binding: binding, desired: ""},
		"retiring binding": {
			binding: RetiringSchedulerBinding(binding, "sha256:current"),
			desired: "sha256:current",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := AdoptLegacySchedulerBindingConfigHash(test.binding, test.desired)
			if ok || got != test.binding {
				t.Fatalf("AdoptLegacySchedulerBindingConfigHash = %#v/%v, want unchanged/false", got, ok)
			}
		})
	}
}

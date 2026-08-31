package api

import (
	"testing"

	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestDecodeProjectSchedulerSpecMapsPersistedValues(t *testing.T) {
	spec, err := decodeProjectSchedulerSpec(`{
		"enabled":true,
		"sandbox_policy":"new",
		"concurrency_policy":"skip",
		"run_timeout":"2h",
		"script":"run()",
		"triggers":[{"name":"accepted","kind":"event","event":{"topic":"ui.accepted"},"sandbox_policy":"sticky"}]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if spec.GetSandboxPolicy() != agentcomposev2.SchedulerSandboxPolicy_SCHEDULER_SANDBOX_POLICY_NEW {
		t.Fatalf("sandbox policy = %v", spec.GetSandboxPolicy())
	}
	if spec.GetConcurrencyPolicy() != agentcomposev2.SchedulerConcurrencyPolicy_SCHEDULER_CONCURRENCY_POLICY_SKIP {
		t.Fatalf("concurrency policy = %v", spec.GetConcurrencyPolicy())
	}
	if spec.GetRunTimeout() != "2h" {
		t.Fatalf("run timeout = %q, want 2h", spec.GetRunTimeout())
	}
	trigger := spec.GetTriggers()[0]
	if trigger.GetKind() != agentcomposev2.TriggerKind_TRIGGER_KIND_EVENT || trigger.GetEvent().GetTopic() != "ui.accepted" ||
		trigger.GetSandboxPolicy() != agentcomposev2.SchedulerSandboxPolicy_SCHEDULER_SANDBOX_POLICY_STICKY {
		t.Fatalf("trigger = %#v", trigger)
	}
}

func TestDecodeProjectSchedulerSpecRejectsInvalidJSON(t *testing.T) {
	if _, err := decodeProjectSchedulerSpec(`{bad`); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

package runs

import (
	"context"
	"strings"
	"testing"
	"time"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/schedulers"
)

func manualTriggerCaptureFixture(script string) (*Controller, domain.Scheduler, *domain.SchedulerTrigger) {
	controller := &Controller{schedulerEngine: &schedulers.QJSSchedulerEngine{}}
	definition := domain.Scheduler{
		Summary: domain.SchedulerSummary{Runtime: domain.SchedulerRuntimeScheduler},
		Script:  script,
	}
	return controller, definition, &domain.SchedulerTrigger{ID: "daily", Kind: domain.SchedulerTriggerKindCron}
}

// A trigger may reach scheduler.agent through the async binding, which calls
// the host from its own goroutine rather than inline.
func TestCaptureManualTriggerAgentRequestAcceptsSingleAsyncAgentCall(t *testing.T) {
	controller, definition, trigger := manualTriggerCaptureFixture(`
scheduler.cron("0 0 * * *", async function daily() {
  await scheduler.agent.async("review today's runs");
}, { id: "daily" });`)

	captured, err := controller.captureManualTriggerAgentRequest(context.Background(), definition, trigger, `{}`)
	if err != nil {
		t.Fatalf("captureManualTriggerAgentRequest returned error: %v", err)
	}
	if captured.prompt != "review today's runs" {
		t.Fatalf("captured prompt = %q, want the async call's prompt", captured.prompt)
	}
}

// Several async agent calls reach the capture host concurrently; the count must
// still be exact so the exactly-once contract is enforced rather than raced.
func TestCaptureManualTriggerAgentRequestRejectsConcurrentAsyncAgentCalls(t *testing.T) {
	controller, definition, trigger := manualTriggerCaptureFixture(`
scheduler.cron("0 0 * * *", async function daily() {
  const prompts = ["a", "b", "c", "d", "e", "f", "g", "h"];
  await Promise.all(prompts.map(function (p) { return scheduler.agent.async(p); }));
}, { id: "daily" });`)

	_, err := controller.captureManualTriggerAgentRequest(context.Background(), definition, trigger, `{}`)
	if err == nil {
		t.Fatalf("captureManualTriggerAgentRequest accepted eight agent calls, want an error")
	}
	if !strings.Contains(err.Error(), "exactly once") {
		t.Fatalf("error = %v, want the exactly-once rejection", err)
	}
}

// Prompt capture is a dry run: it evaluates the trigger only to observe the
// agent request it would make. A sleep in the callback must not make the
// resolve API wait it out.
func TestCaptureManualTriggerAgentRequestSkipsSleeps(t *testing.T) {
	controller, definition, trigger := manualTriggerCaptureFixture(`
scheduler.cron("0 0 * * *", async function daily() {
  scheduler.sleep(20000);
  await scheduler.agent("review today's runs");
}, { id: "daily" });`)

	start := time.Now()
	captured, err := controller.captureManualTriggerAgentRequest(context.Background(), definition, trigger, `{}`)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("captureManualTriggerAgentRequest returned error: %v", err)
	}
	if captured.prompt != "review today's runs" {
		t.Fatalf("captured prompt = %q", captured.prompt)
	}
	if elapsed > 15*time.Second {
		t.Fatalf("captureManualTriggerAgentRequest took %v; it waited out the script's sleep", elapsed)
	}
}

// A trigger may start an agent through the async binding and never await it.
// Capture must still see exactly one call: prompt resolution is a dry run, so
// its outcome cannot depend on whether the goroutine beat teardown to the host.
func TestCaptureManualTriggerAgentRequestIsDeterministicForUnawaitedAsyncAgent(t *testing.T) {
	for i := range 20 {
		controller, definition, trigger := manualTriggerCaptureFixture(`
scheduler.cron("0 0 * * *", async function daily() {
  scheduler.agent.async("review today's runs");
}, { id: "daily" });`)

		captured, err := controller.captureManualTriggerAgentRequest(context.Background(), definition, trigger, `{}`)
		if err != nil {
			t.Fatalf("iteration %d: captureManualTriggerAgentRequest returned error: %v", i, err)
		}
		if captured.prompt != "review today's runs" {
			t.Fatalf("iteration %d: captured prompt = %q", i, captured.prompt)
		}
	}
}

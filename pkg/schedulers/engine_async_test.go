package schedulers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	domain "agent-compose/pkg/model"
)

func TestSchedulerLLMAsyncResolvesToSameResultAsSyncCall(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  const syncResult = scheduler.llm("answer");
  const asyncResult = await scheduler.llm.async("answer");
  return { sync: syncResult.text, async: asyncResult.text };
}`,
	}, &coverageEngineHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	const want = `{"sync":"llm-output","async":"llm-output"}`
	if result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
}

// concurrencyProbeHost records how many LLM calls are in flight at once, so
// tests can assert on real overlap instead of on wall-clock timing.
type concurrencyProbeHost struct {
	coverageEngineHost
	delay time.Duration

	mu      sync.Mutex
	live    int
	peak    int
	nCalls  int
	prompts []string
}

func (h *concurrencyProbeHost) LLM(_ context.Context, prompt string, _ domain.SchedulerLLMRequest) (domain.SchedulerLLMResult, error) {
	h.mu.Lock()
	h.live++
	h.nCalls++
	h.prompts = append(h.prompts, prompt)
	if h.live > h.peak {
		h.peak = h.live
	}
	h.mu.Unlock()

	time.Sleep(h.delay)

	h.mu.Lock()
	h.live--
	h.mu.Unlock()
	return domain.SchedulerLLMResult{Text: "out:" + prompt}, nil
}

func (h *concurrencyProbeHost) peakInFlight() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.peak
}

func TestSchedulerLLMAsyncRunsPromiseAllBatchConcurrently(t *testing.T) {
	host := &concurrencyProbeHost{delay: 50 * time.Millisecond}
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  const results = await Promise.all(["a", "b", "c"].map(p => scheduler.llm.async(p)));
  return results.map(r => r.text);
}`,
	}, host)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if want := `["out:a","out:b","out:c"]`; result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
	if peak := host.peakInFlight(); peak != 3 {
		t.Fatalf("peak in-flight LLM calls = %d, want 3 (1 means the batch ran serially)", peak)
	}
}

func (h *concurrencyProbeHost) liveInFlight() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.live
}

func TestSchedulerLLMAsyncDrainsUnawaitedCallsBeforeExecuteReturns(t *testing.T) {
	host := &concurrencyProbeHost{delay: 50 * time.Millisecond}
	_, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  scheduler.llm.async("orphan");
  return "returned without awaiting";
}`,
	}, host)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	// A handle the script never awaits must still be settled before the run
	// finishes: otherwise the host keeps recording events for a run that has
	// already been torn down.
	if live := host.liveInFlight(); live != 0 {
		t.Fatalf("in-flight LLM calls after Execute returned = %d, want 0", live)
	}
}

func TestSchedulerLLMAsyncRespectsMaxAsyncConcurrency(t *testing.T) {
	host := &concurrencyProbeHost{delay: 30 * time.Millisecond}
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime:                domain.SchedulerRuntimeScheduler,
		MaxAsyncLLMConcurrency: 2,
		Script: `
async function main() {
  const prompts = ["a", "b", "c", "d", "e", "f"];
  const results = await Promise.all(prompts.map(p => scheduler.llm.async(p)));
  return results.length;
}`,
	}, host)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.ResultJSON != "6" {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, "6")
	}
	if peak := host.peakInFlight(); peak > 2 {
		t.Fatalf("peak in-flight LLM calls = %d, want at most 2", peak)
	}
}

type failingLLMHost struct{ coverageEngineHost }

func (h *failingLLMHost) LLM(context.Context, string, domain.SchedulerLLMRequest) (domain.SchedulerLLMResult, error) {
	return domain.SchedulerLLMResult{}, errors.New("llm upstream exploded")
}

func TestSchedulerLLMAsyncHostErrorRejectsAndIsCatchable(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  try {
    await scheduler.llm.async("x");
    return "resolved without rejecting";
  } catch (e) {
    return "caught: " + String(e);
  }
}`,
	}, &failingLLMHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.ResultJSON, "llm upstream exploded") {
		t.Fatalf("ResultJSON = %q, want a caught rejection mentioning the host error", result.ResultJSON)
	}
}

func TestSchedulerLLMAsyncHandleSupportsCatch(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  return await scheduler.llm.async("x").catch(function (e) { return "caught: " + String(e); });
}`,
	}, &failingLLMHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.ResultJSON, "llm upstream exploded") {
		t.Fatalf("ResultJSON = %q, want the rejection handled by .catch()", result.ResultJSON)
	}
}

// ctxIgnoringHost blocks until the test releases it, ignoring cancellation, to
// model a downstream client that does not honour ctx. entered reports when a
// call is genuinely in progress, so tests cancel at a known point instead of
// racing engine start-up.
type ctxIgnoringHost struct {
	coverageEngineHost
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (h *ctxIgnoringHost) LLM(context.Context, string, domain.SchedulerLLMRequest) (domain.SchedulerLLMResult, error) {
	h.once.Do(func() { close(h.entered) })
	<-h.release
	return domain.SchedulerLLMResult{Text: "late"}, nil
}

// StateGet blocks until an async LLM call is genuinely inside the host, giving
// a script a synchronous way to wait for that without racing goroutine start-up.
func (h *ctxIgnoringHost) StateGet(context.Context, string) (string, bool, error) {
	<-h.entered
	return "", false, nil
}

func TestSchedulerLLMAsyncDoesNotWedgeRunWhenHostIgnoresCancellation(t *testing.T) {
	host := &ctxIgnoringHost{release: make(chan struct{}), entered: make(chan struct{})}
	defer close(host.release)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		// Cancel only once the host call is actually in progress, so the test
		// exercises cancellation mid-call rather than during engine start-up.
		<-host.entered
		cancel()
	}()

	start := time.Now()
	_, err := (&QJSSchedulerEngine{}).Execute(ctx, SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  await scheduler.llm.async("x");
  return "should not get here";
}`,
	}, host)
	elapsed := time.Since(start)

	select {
	case <-host.entered:
	default:
		t.Fatalf("host LLM was never called; the test did not reach the cancellation path")
	}
	if err == nil {
		t.Fatalf("Execute returned no error, want a cancellation error")
	}
	// The run must give up on a host call that ignores cancellation instead of
	// waiting on it forever; otherwise one bad downstream wedges the scheduler.
	if elapsed > 20*time.Second {
		t.Fatalf("Execute took %v after cancellation; the run was wedged", elapsed)
	}
}

// variableDelayHost derives its latency from the prompt so tests can express
// "this one is slower" without depending on wall-clock tuning.
type variableDelayHost struct {
	coverageEngineHost
	mu      sync.Mutex
	prompts []string
}

func (h *variableDelayHost) LLM(_ context.Context, prompt string, request domain.SchedulerLLMRequest) (domain.SchedulerLLMResult, error) {
	h.mu.Lock()
	h.prompts = append(h.prompts, prompt)
	h.mu.Unlock()
	if strings.HasPrefix(prompt, "slow") {
		time.Sleep(300 * time.Millisecond)
	} else {
		time.Sleep(10 * time.Millisecond)
	}
	if strings.HasPrefix(prompt, "boom") {
		return domain.SchedulerLLMResult{}, errors.New("upstream refused " + prompt)
	}
	return domain.SchedulerLLMResult{Text: "out:" + prompt, Model: request.Model}, nil
}

func TestSchedulerLLMRaceResolvesWithFastestCallNotFirstListed(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  const winner = await scheduler.llm.race(["slow-one", "fast-one"]);
  return winner.text;
}`,
	}, &variableDelayHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	// Standard Promise.race settles with whichever thenable the job queue
	// reaches first, which is the first array entry. scheduler.llm.race must
	// settle with the call that actually finished first.
	if want := `"out:fast-one"`; result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
}

func TestSchedulerLLMAnySkipsFastFailureAndResolvesWithSlowSuccess(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  const winner = await scheduler.llm.any(["boom-fast", "slow-one"]);
  return winner.text;
}`,
	}, &variableDelayHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	// The fast entry settles first but rejects; any must keep waiting for a
	// fulfilled call rather than adopting the first settled one.
	if want := `"out:slow-one"`; result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
}

func TestSchedulerLLMAnyRejectsWhenEveryCallFails(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  try {
    await scheduler.llm.any(["boom-a", "boom-b"]);
    return "resolved without rejecting";
  } catch (e) {
    return "caught: " + String(e);
  }
}`,
	}, &variableDelayHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.ResultJSON, "upstream refused") {
		t.Fatalf("ResultJSON = %q, want a rejection after every call failed", result.ResultJSON)
	}
}

func TestSchedulerSleepBlocksForTheRequestedDuration(t *testing.T) {
	// Measured inside the script so engine start-up cannot mask a no-op sleep.
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  const startedAt = Date.now();
  scheduler.sleep(80);
  return Date.now() - startedAt >= 80;
}`,
	}, &coverageEngineHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.ResultJSON != "true" {
		t.Fatalf("ResultJSON = %q, want %q (scheduler.sleep did not delay)", result.ResultJSON, "true")
	}
}

func TestSchedulerSleepAsyncOverlapsInsidePromiseAll(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  const startedAt = Date.now();
  await Promise.all([scheduler.sleep.async(150), scheduler.sleep.async(150)]);
  const elapsed = Date.now() - startedAt;
  return { overlapped: elapsed < 280, elapsedAtLeast: elapsed >= 150 };
}`,
	}, &coverageEngineHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if want := `{"overlapped":true,"elapsedAtLeast":true}`; result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
}

// agentProbeHost records agent concurrency and the sandbox policy each call
// resolved to.
type agentProbeHost struct {
	coverageEngineHost
	delay time.Duration

	mu       sync.Mutex
	live     int
	peak     int
	policies []string
}

func (h *agentProbeHost) Agent(_ context.Context, prompt string, request domain.SchedulerAgentRequest) (domain.SchedulerAgentResult, error) {
	h.mu.Lock()
	h.live++
	if h.live > h.peak {
		h.peak = h.live
	}
	h.policies = append(h.policies, request.SandboxPolicy)
	h.mu.Unlock()

	time.Sleep(h.delay)

	h.mu.Lock()
	h.live--
	h.mu.Unlock()
	return domain.SchedulerAgentResult{Text: "agent:" + prompt, FinalText: "agent:" + prompt, Success: true}, nil
}

func (h *agentProbeHost) snapshot() (int, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.peak, append([]string(nil), h.policies...)
}

func TestSchedulerAgentAsyncRunsBatchConcurrentlyWithFreshSandboxes(t *testing.T) {
	host := &agentProbeHost{delay: 50 * time.Millisecond}
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  const results = await Promise.all(["a", "b", "c"].map(p => scheduler.agent.async(p)));
  return results.map(r => r.text);
}`,
	}, host)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if want := `["agent:a","agent:b","agent:c"]`; result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
	peak, policies := host.snapshot()
	if peak != 3 {
		t.Fatalf("peak in-flight agent calls = %d, want 3", peak)
	}
	// Parallel agents cannot share a sticky sandbox: concurrent runs would
	// write the same workspace, and the first to finish shuts the sandbox down.
	for i, policy := range policies {
		if policy != domain.SchedulerSandboxPolicyNew {
			t.Fatalf("agent call %d used sandbox policy %q, want %q", i, policy, domain.SchedulerSandboxPolicyNew)
		}
	}
}

func TestSchedulerAgentAsyncRejectsStickySandboxPolicy(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  try {
    await scheduler.agent.async("a", { sandboxPolicy: "sticky" });
    return "accepted sticky";
  } catch (e) {
    return "caught: " + String(e);
  }
}`,
	}, &agentProbeHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.ResultJSON, "sandboxPolicy") {
		t.Fatalf("ResultJSON = %q, want a rejection naming sandboxPolicy", result.ResultJSON)
	}
}

func TestSchedulerAgentAsyncUsesConservativeDefaultConcurrency(t *testing.T) {
	host := &agentProbeHost{delay: 30 * time.Millisecond}
	_, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  const prompts = ["a", "b", "c", "d", "e", "f"];
  return (await Promise.all(prompts.map(p => scheduler.agent.async(p)))).length;
}`,
	}, host)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	// Each parallel agent is a fresh sandbox, so the default ceiling is much
	// lower than for plain LLM calls: sandbox creation has no limit of its own
	// and the SQLite pool defaults to four connections.
	peak, _ := host.snapshot()
	if peak > DefaultMaxAsyncAgentConcurrency {
		t.Fatalf("peak in-flight agent calls = %d, want at most %d", peak, DefaultMaxAsyncAgentConcurrency)
	}
}

func TestAsyncConcurrencyFromSchedulerEnv(t *testing.T) {
	env := func(pairs ...string) []domain.SandboxEnvVar {
		items := make([]domain.SandboxEnvVar, 0, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			items = append(items, domain.SandboxEnvVar{Name: pairs[i], Value: pairs[i+1]})
		}
		return items
	}
	for _, tc := range []struct {
		name      string
		items     []domain.SandboxEnvVar
		wantLLM   int
		wantAgent int
	}{
		{name: "unset falls back to zero so the engine picks its defaults"},
		{name: "both set", items: env("LLM_MAX_CONCURRENCY", "12", "AGENT_MAX_CONCURRENCY", "2"), wantLLM: 12, wantAgent: 2},
		{name: "only llm set", items: env("LLM_MAX_CONCURRENCY", "4"), wantLLM: 4},
		{name: "name matching ignores case", items: env("llm_max_concurrency", "5"), wantLLM: 5},
		{name: "non numeric is ignored", items: env("LLM_MAX_CONCURRENCY", "lots")},
		{name: "non positive is ignored", items: env("AGENT_MAX_CONCURRENCY", "0")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			llm, agent := AsyncConcurrencyFromSchedulerEnv(tc.items)
			if llm != tc.wantLLM || agent != tc.wantAgent {
				t.Fatalf("AsyncConcurrencyFromSchedulerEnv() = (%d, %d), want (%d, %d)", llm, agent, tc.wantLLM, tc.wantAgent)
			}
		})
	}
}

// schemaRaceHost answers the fast prompt with JSON and the slow one with plain
// text, so a test can tell which entry's outputSchema was applied.
type schemaRaceHost struct{ coverageEngineHost }

func (h *schemaRaceHost) LLM(_ context.Context, prompt string, _ domain.SchedulerLLMRequest) (domain.SchedulerLLMResult, error) {
	if strings.HasPrefix(prompt, "slow") {
		time.Sleep(300 * time.Millisecond)
		return domain.SchedulerLLMResult{Text: "plain text"}, nil
	}
	time.Sleep(10 * time.Millisecond)
	return domain.SchedulerLLMResult{Text: `{"ok":true}`}, nil
}

func TestSchedulerLLMRaceAppliesTheWinnersOutputSchema(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  const Shape = scheduler.z.object({ ok: scheduler.z.boolean() });
  const winner = await scheduler.llm.race([
    "slow-plain",
    { prompt: "fast-json", outputSchema: Shape },
  ]);
  return { json: winner.json };
}`,
	}, &schemaRaceHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	// The winning entry carries the schema; encoding it with the first entry's
	// (absent) schema would silently drop the parsed json field.
	if want := `{"json":{"ok":true}}`; result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
}

func TestSchedulerSleepDoesNotBlockValidation(t *testing.T) {
	// Validation evaluates the script's top level to collect triggers. A sleep
	// there must not hold saving the scheduler hostage for its full duration.
	done := make(chan error, 1)
	go func() {
		_, err := (&QJSSchedulerEngine{}).Validate(context.Background(), domain.SchedulerRuntimeScheduler, `
scheduler.sleep(600000);
function main() { return "ok"; }`)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Validate returned error: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("Validate did not return; a top-level scheduler.sleep blocked it")
	}
}

func TestSchedulerLLMAsyncAppliesInlineOutputSchema(t *testing.T) {
	// The schema value is read during the binding call but used when the
	// thenable settles. Nothing in the script retains it here, so this locks in
	// that holding it across the suspension is safe.
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  const answer = await scheduler.llm.async("answer", {
    outputSchema: scheduler.z.object({ summary: scheduler.z.string(), risk: scheduler.z.enum(["low", "high"]) }),
  });
  return answer.json;
}`,
	}, &coverageEngineHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if want := `{"risk":"low","summary":"ok"}`; result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
}

func TestSchedulerSleepRejectsDurationThatOverflows(t *testing.T) {
	// time.Duration(ms) * time.Millisecond overflows int64 well before the
	// number itself does, turning an absurd request into an instant no-op or an
	// arbitrary wait instead of an error.
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  try {
    scheduler.sleep(9223372036855);
    return "accepted";
  } catch (e) {
    return "caught: " + String(e);
  }
}`,
	}, &coverageEngineHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.ResultJSON, "caught:") {
		t.Fatalf("ResultJSON = %q, want an error for an out-of-range duration", result.ResultJSON)
	}
}

func TestSchedulerLLMRaceRejectsNonPromptEntries(t *testing.T) {
	for _, entry := range []string{"null", "42", `["a"]`, "undefined"} {
		t.Run(entry, func(t *testing.T) {
			host := &concurrencyProbeHost{}
			result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
				Runtime: domain.SchedulerRuntimeScheduler,
				Script: `
async function main() {
  try {
    await scheduler.llm.race([` + entry + `]);
    return "accepted";
  } catch (e) {
    return "caught: " + String(e);
  }
}`,
			}, host)
			if err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}
			if !strings.Contains(result.ResultJSON, "caught:") {
				t.Fatalf("ResultJSON = %q, want an error for a non-prompt entry", result.ResultJSON)
			}
			// A rejected entry must never reach the provider: these calls bill.
			if _, prompts := host.snapshotPrompts(); len(prompts) != 0 {
				t.Fatalf("host received prompts %v, want none", prompts)
			}
		})
	}
}

func (h *concurrencyProbeHost) snapshotPrompts() (int, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nCalls, append([]string(nil), h.prompts...)
}

func TestSchedulerSleepAsyncDoesNotHoldTheRunOpenWhenNotAwaited(t *testing.T) {
	start := time.Now()
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  scheduler.sleep.async(30000);
  return "returned without awaiting";
}`,
	}, &coverageEngineHost{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.ResultJSON != `"returned without awaiting"` {
		t.Fatalf("ResultJSON = %q", result.ResultJSON)
	}
	// A sleep records nothing with the host, so unlike a real host call there is
	// nothing to drain at teardown; joining one would let a script stall its own
	// run long after main() returned.
	if elapsed > 20*time.Second {
		t.Fatalf("Execute took %v; an unawaited sleep held the run open", elapsed)
	}
}

// raceLoserHost answers the fast prompt at once and makes the slow one block
// until either the test releases it or its context is cancelled.
type raceLoserHost struct {
	coverageEngineHost
	release chan struct{}
}

func (h *raceLoserHost) LLM(ctx context.Context, prompt string, _ domain.SchedulerLLMRequest) (domain.SchedulerLLMResult, error) {
	if strings.HasPrefix(prompt, "slow") {
		select {
		case <-h.release:
		case <-ctx.Done():
			return domain.SchedulerLLMResult{}, ctx.Err()
		}
	}
	return domain.SchedulerLLMResult{Text: "out:" + prompt}, nil
}

func TestSchedulerLLMRaceCancelsLosingCalls(t *testing.T) {
	host := &raceLoserHost{release: make(chan struct{})}
	defer close(host.release)

	start := time.Now()
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  const winner = await scheduler.llm.race(["slow-a", "fast-b"]);
  return winner.text;
}`,
	}, host)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if want := `"out:fast-b"`; result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
	// The loser is never released by the test, so the run can only finish if
	// deciding a winner cancelled it. Otherwise draining waits on it forever.
	if elapsed > 25*time.Second {
		t.Fatalf("Execute took %v; the losing call was not cancelled", elapsed)
	}
}

func TestSchedulerAsyncUsesCapturedPromiseResolve(t *testing.T) {
	// The engine already captures JSON.stringify before user code runs so a
	// script cannot change its serialization. Promise adoption needs the same
	// treatment: reading Promise.resolve off the global at call time lets a
	// script decide what every async binding hands back.
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  Promise.resolve = function () { return "hijacked"; };
  const answer = await scheduler.llm.async("answer");
  return answer.text;
}`,
	}, &coverageEngineHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if want := `"llm-output"`; result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
}

func TestSchedulerLLMRaceCancelsLosersEvenWhenNeverAwaited(t *testing.T) {
	host := &raceLoserHost{release: make(chan struct{})}
	defer close(host.release)

	start := time.Now()
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  scheduler.llm.race(["slow-a", "fast-b"]);
  return "never awaited the race";
}`,
	}, host)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.ResultJSON != `"never awaited the race"` {
		t.Fatalf("ResultJSON = %q", result.ResultJSON)
	}
	// Cancelling the losers only when the group settles leaves them running
	// whenever the script never awaits the promise, so the run pays their full
	// latency and the failover property is lost exactly when it is least
	// visible.
	if elapsed > 25*time.Second {
		t.Fatalf("Execute took %v; the losing calls outlived an abandoned race", elapsed)
	}
}

// blockingFirstCallHost holds only its first call for a while and answers the
// rest at once. With a concurrency limit of one that keeps every later handle
// parked, so handles accumulate as genuinely outstanding instead of settling as
// fast as the script creates them -- and the run still finishes promptly.
type blockingFirstCallHost struct {
	coverageEngineHost
	hold time.Duration
	once sync.Once
}

func (h *blockingFirstCallHost) LLM(ctx context.Context, prompt string, _ domain.SchedulerLLMRequest) (domain.SchedulerLLMResult, error) {
	h.once.Do(func() {
		select {
		case <-time.After(h.hold):
		case <-ctx.Done():
		}
	})
	return domain.SchedulerLLMResult{Text: "out:" + prompt}, nil
}

func TestSchedulerLLMAsyncRejectsFanOutBeyondOutstandingLimit(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime:                domain.SchedulerRuntimeScheduler,
		MaxAsyncLLMConcurrency: 1,
		Script: fmt.Sprintf(`
async function main() {
  const handles = [];
  try {
    for (let i = 0; i < %d; i++) handles.push(scheduler.llm.async("p" + i));
    return "accepted";
  } catch (e) {
    return "caught: " + String(e);
  }
}`, maxOutstandingAsyncCalls+1),
	}, &blockingFirstCallHost{hold: 8 * time.Second})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	// The concurrency limit gates how many calls run at once, but each pending
	// handle still costs a goroutine and its stack, which lives outside the
	// QuickJS memory limit. Without a ceiling a long array turns script input
	// straight into hundreds of megabytes.
	if !strings.Contains(result.ResultJSON, "calls outstanding") {
		t.Fatalf("ResultJSON = %q, want the outstanding-calls rejection", result.ResultJSON)
	}
}

// panickingHost models a host implementation that panics rather than returning
// an error. The synchronous bindings survive it because qjs turns a panic
// inside a proxied function into a JS exception; an async call runs on its own
// goroutine, where an unrecovered panic would take the whole daemon down.
type panickingHost struct{ coverageEngineHost }

func (h *panickingHost) LLM(context.Context, string, domain.SchedulerLLMRequest) (domain.SchedulerLLMResult, error) {
	panic("host exploded")
}

func TestSchedulerLLMAsyncTurnsHostPanicIntoRejection(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  try {
    await scheduler.llm.async("x");
    return "resolved without rejecting";
  } catch (e) {
    return "caught: " + String(e);
  }
}`,
	}, &panickingHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.ResultJSON, "host exploded") {
		t.Fatalf("ResultJSON = %q, want the panic surfaced as a rejection", result.ResultJSON)
	}
}

func TestSchedulerLLMRaceRejectsGroupLargerThanConcurrencyBudget(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime:                domain.SchedulerRuntimeScheduler,
		MaxAsyncLLMConcurrency: 1,
		Script: `
async function main() {
  try {
    const winner = await scheduler.llm.race(["slow-one", "fast-one"]);
    return "accepted: " + winner.text;
  } catch (e) {
    return "caught: " + String(e);
  }
}`,
	}, &variableDelayHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	// A group whose entries cannot all be in flight cannot race: the later ones
	// only start as earlier ones finish, so the combinator degrades into the
	// "first listed" behaviour it exists to avoid. Failing loudly beats
	// returning a plausible but wrong winner.
	if !strings.Contains(result.ResultJSON, "caught:") {
		t.Fatalf("ResultJSON = %q, want a rejection when the budget cannot hold the group", result.ResultJSON)
	}
}

func TestSchedulerLLMRaceStillPicksFastestWhenBudgetExactlyFitsGroup(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime:                domain.SchedulerRuntimeScheduler,
		MaxAsyncLLMConcurrency: 2,
		Script: `
async function main() {
  return (await scheduler.llm.race(["slow-one", "fast-one"])).text;
}`,
	}, &variableDelayHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if want := `"out:fast-one"`; result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
}

func TestSchedulerLLMAsyncHandleCanBeAwaitedRepeatedly(t *testing.T) {
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  const handle = scheduler.llm.async("answer");
  const first = await handle;
  const second = await handle;
  return [first.text, second.text];
}`,
	}, &coverageEngineHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if want := `["llm-output","llm-output"]`; result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
}

// ctxAwareSlowHost takes a long time but honours cancellation. When entered is
// set it reports each arrival, so a script can wait for calls to be genuinely
// inside the host -- and therefore holding their concurrency slots -- instead of
// racing goroutine start-up.
type ctxAwareSlowHost struct {
	coverageEngineHost
	hold    time.Duration
	entered chan struct{}
}

func (h *ctxAwareSlowHost) LLM(ctx context.Context, prompt string, _ domain.SchedulerLLMRequest) (domain.SchedulerLLMResult, error) {
	if h.entered != nil {
		h.entered <- struct{}{}
	}
	select {
	case <-time.After(h.hold):
		return domain.SchedulerLLMResult{Text: "out:" + prompt}, nil
	case <-ctx.Done():
		return domain.SchedulerLLMResult{}, ctx.Err()
	}
}

// StateGet blocks until the number of calls named by key have entered the host.
func (h *ctxAwareSlowHost) StateGet(_ context.Context, key string) (string, bool, error) {
	want, err := strconv.Atoi(strings.TrimSpace(key))
	if err != nil {
		return "", false, err
	}
	for range want {
		<-h.entered
	}
	return "", false, nil
}

func TestSchedulerLLMAsyncCancelsUnawaitedCallsInsteadOfWaitingThemOut(t *testing.T) {
	host := &ctxAwareSlowHost{hold: 60 * time.Second}

	start := time.Now()
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  scheduler.llm.async("slow");
  return "returned without awaiting";
}`,
	}, host)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.ResultJSON != `"returned without awaiting"` {
		t.Fatalf("ResultJSON = %q", result.ResultJSON)
	}
	// Draining runs after the script has finished, so anything still in flight
	// is abandoned by definition. Waiting it out holds the scheduler's run slot
	// -- leaveRun is deferred until Execute returns -- so a fire-and-forget call
	// would keep skipping later triggers under concurrency_policy: skip.
	if elapsed > 30*time.Second {
		t.Fatalf("Execute took %v; an abandoned call was waited out instead of cancelled", elapsed)
	}
}

func TestSchedulerLLMAsyncAbandonsUnawaitedCallThatIgnoresCancellation(t *testing.T) {
	host := &ctxIgnoringHost{release: make(chan struct{}), entered: make(chan struct{})}
	defer close(host.release)

	start := time.Now()
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  scheduler.llm.async("ignores-cancellation");
  // Blocks until that call is inside the host, so the run reaches teardown
  // with a genuinely in-flight call rather than one cancelled before it began.
  scheduler.state.get("wait-for-async-call");
  return "returned without awaiting";
}`,
	}, host)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.ResultJSON != `"returned without awaiting"` {
		t.Fatalf("ResultJSON = %q", result.ResultJSON)
	}
	select {
	case <-host.entered:
	default:
		t.Fatalf("host LLM was never called; the test did not reach the drain path")
	}
	// The run itself is never cancelled here, so a drain that only bounds its
	// wait after cancellation blocks forever on a downstream that ignores ctx.
	if elapsed > 30*time.Second {
		t.Fatalf("Execute took %v; the drain waited on a call that ignores cancellation", elapsed)
	}
}

func TestSchedulerLLMRaceRejectsWhenBudgetIsHeldByUnawaitedCalls(t *testing.T) {
	host := &ctxAwareSlowHost{hold: 60 * time.Second, entered: make(chan struct{}, 4)}

	start := time.Now()
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime:                domain.SchedulerRuntimeScheduler,
		MaxAsyncLLMConcurrency: 2,
		Script: `
async function main() {
  scheduler.llm.async("hold-a");
  scheduler.llm.async("hold-b");
  // Blocks until both are inside the host, so the budget is genuinely held
  // rather than merely about to be.
  scheduler.state.get("2");
  try {
    await scheduler.llm.race(["x", "y"]);
    return "accepted";
  } catch (e) {
    return "caught: " + String(e);
  }
}`,
	}, host)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	// Taking the slots must not block the JS thread: QuickJS only interrupts on
	// bytecode, so its execution-time limit cannot break a Go channel wait, and
	// the run would hang until its deadline with no way to report why.
	if elapsed > 30*time.Second {
		t.Fatalf("Execute took %v; reserving race slots blocked the JS thread", elapsed)
	}
	if !strings.Contains(result.ResultJSON, "caught:") {
		t.Fatalf("ResultJSON = %q, want a rejection when the budget is already held", result.ResultJSON)
	}
}

func TestSchedulerLLMAsyncAbandonedHandleDelaysLaterAwait(t *testing.T) {
	// Adopting each handle with Promise.resolve enqueues its thenable job at
	// creation, and the job queue is drained in order with every job blocking.
	// An abandoned handle created first therefore has to settle before a later
	// await can proceed. This pins that contract so the cost of the design is
	// visible, and so a future engine with a real event loop shows up here.
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime: domain.SchedulerRuntimeScheduler,
		Script: `
async function main() {
  const startedAt = Date.now();
  scheduler.llm.async("slow-abandoned");
  const wanted = await scheduler.llm.async("fast-wanted");
  return { text: wanted.text, waitedForAbandoned: Date.now() - startedAt >= 300 };
}`,
	}, &variableDelayHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if want := `{"text":"out:fast-wanted","waitedForAbandoned":true}`; result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
}

func TestSchedulerLLMRaceReleasesSlotsOnDryRuns(t *testing.T) {
	// A dry run runs async calls inline, which skips the goroutine that would
	// otherwise release the slot a racing group reserved for each entry. Two
	// groups in one dry run therefore have to work: leaking would exhaust the
	// budget and fail the second with a confusing capacity error.
	result, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
		Runtime:                domain.SchedulerRuntimeScheduler,
		DryRun:                 true,
		MaxAsyncLLMConcurrency: 2,
		Script: `
async function main() {
  const first = await scheduler.llm.race(["a1", "a2"]);
  const second = await scheduler.llm.race(["b1", "b2"]);
  return [first.text, second.text];
}`,
	}, &coverageEngineHost{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if want := `["llm-output","llm-output"]`; result.ResultJSON != want {
		t.Fatalf("ResultJSON = %q, want %q", result.ResultJSON, want)
	}
}

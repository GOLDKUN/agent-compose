package schedulers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	domain "agent-compose/pkg/model"

	"github.com/fastschema/qjs"
)

// asyncLLMCall is one in-flight scheduler.llm.async call.
type asyncLLMCall = asyncCall[domain.SchedulerLLMResult]

// asyncLLMRace groups calls started together so the winner can be taken in
// completion order. Standard Promise.race cannot do this here: its thenable
// jobs run in queue order and each one blocks, so it always settles with the
// first entry rather than the fastest.
//
// "First" means the first completion this group observes, not the strictly
// earliest one: an entry reports itself on finished just after closing its done
// channel, and preemption between those two steps can let a marginally slower
// entry report first. That matches Promise semantics everywhere -- no
// implementation promises strict completion time, only observation order -- and
// the two suggested alternatives do not improve on it. Selecting over the done
// channels picks pseudo-randomly among ready cases, and taking the lowest
// completion sequence number would mean waiting for every entry, which is the
// opposite of racing.
type asyncLLMRace struct {
	finished chan *asyncLLMCall
	// byCall maps each started call back to the entry it came from, because
	// entries carry their own model and outputSchema and the winner must be
	// encoded with its own, not with the first entry's.
	byCall map[*asyncLLMCall]schedulerLLMInvocation
	total  int
	// reported counts entries taken off finished so far, so what is left can be
	// reaped once the group has settled.
	reported int
	// cancel stops the entries that did not win. Without it the run keeps
	// paying for them: draining waits on every started call, so a failover
	// racing a hung provider would still cost that provider's full latency.
	cancel context.CancelFunc
}

// await returns the winning call together with the entry it came from. With
// requireSuccess it keeps consuming completions until one succeeded, and
// reports the last failure if none did.
func (r *asyncLLMRace) await(ctx context.Context, requireSuccess bool) (*asyncLLMCall, schedulerLLMInvocation, error) {
	var lastErr error
	for received := 0; received < r.total; received++ {
		var call *asyncLLMCall
		select {
		case call = <-r.finished:
			r.reported++
		case <-ctx.Done():
			return nil, schedulerLLMInvocation{}, context.Cause(ctx)
		}
		if call.err == nil {
			return call, r.byCall[call], nil
		}
		lastErr = call.err
		if !requireSuccess {
			return nil, schedulerLLMInvocation{}, call.err
		}
	}
	return nil, schedulerLLMInvocation{}, lastErr
}

// releaseSettledResults drops the response bodies the group is still holding.
//
// qjs v0.0.6 never unregisters the Go function behind a thenable, so this
// group -- and everything it captured -- lives until the runtime closes. A
// script that races once per item over a long list would otherwise retain every
// response body of every entry, winner and loser alike. Only settled calls are
// touched: an entry still running owns its own result field.
func (r *asyncLLMRace) releaseSettledResults() {
	for {
		select {
		case call := <-r.finished:
			r.reported++
			r.release(call)
		default:
			for call := range r.byCall {
				delete(r.byCall, call)
			}
			return
		}
	}
}

// reapLateEntries releases the bodies of entries that report after the group
// settled.
//
// Cancelling the losers is asynchronous, so an entry can still be mid-call when
// the group settles and reports a full response afterwards, straight into the
// buffered channel that this group's closure holds. qjs v0.0.6 never
// unregisters that closure, so without a reaper those bodies would sit there
// until the runtime closes -- noticeable for a script running many racing
// batches over large responses. The reaper exits with the execution, since
// nothing is worth reclaiming once the runtime is about to go away.
func (r *asyncLLMRace) reapLateEntries(ctx context.Context) {
	remaining := r.total - r.reported
	if remaining <= 0 {
		return
	}
	go func() {
		for range remaining {
			select {
			case call := <-r.finished:
				r.release(call)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// release drops the response body of a call that has finished.
func (r *asyncLLMRace) release(call *asyncLLMCall) {
	var released domain.SchedulerLLMResult
	call.result = released
}

// schedulerLLMInvocation is one parsed scheduler.llm call.
type schedulerLLMInvocation struct {
	prompt            string
	options           domain.SchedulerLLMRequest
	outputSchemaValue *qjs.Value
}

func (s *schedulerExecutionState) startAsyncLLM(ctx context.Context, apiName string, invocation schedulerLLMInvocation, slotHeld bool, notify chan<- *asyncLLMCall) (*asyncLLMCall, error) {
	return startAsyncHostCall(ctx, s, asyncCallSpec[domain.SchedulerLLMResult]{
		apiName:  apiName,
		slot:     s.llmSem,
		slotHeld: slotHeld,
		notify:   notify,
		work: func(callCtx context.Context) (domain.SchedulerLLMResult, error) {
			return s.host.LLM(callCtx, invocation.prompt, invocation.options)
		},
	})
}

// reserveRaceSlots takes one concurrency slot per entry so that every entry of
// a racing group is genuinely in flight.
//
// Letting entries queue on the shared budget would quietly reduce race and any
// to "first listed": later entries would only start as earlier ones finish,
// which is precisely the Promise.race behaviour these exist to replace. A group
// the budget can never hold is rejected rather than answered with a plausible
// but wrong winner.
func (s *schedulerExecutionState) reserveRaceSlots(apiName string, count int) (*raceSlots, error) {
	if budget := cap(s.llmSem); count > budget {
		return nil, fmt.Errorf(
			"%s cannot race %d entries with a concurrency budget of %d: every entry has to be in flight at once, so raise %s or race fewer entries",
			apiName, count, budget, envMaxAsyncLLMConcurrency,
		)
	}
	slots := &raceSlots{sem: s.llmSem}
	for slots.held < count {
		select {
		case s.llmSem <- struct{}{}:
			slots.held++
		default:
			// Waiting here would block the JS thread inside a Go channel
			// operation, which QuickJS cannot interrupt -- its execution-time
			// limit only fires between bytecodes -- so the run would hang with
			// no way to report why. Refusing is also consistent with rejecting
			// a group larger than the budget: in both cases the entries cannot
			// all be in flight, so the group cannot race.
			slots.releaseUnused()
			return nil, fmt.Errorf(
				"%s needs %d free concurrency slots but only %d of %d are available; await the calls already in flight before racing",
				apiName, count, cap(s.llmSem)-len(s.llmSem), cap(s.llmSem),
			)
		}
	}
	return slots, nil
}

// raceSlots owns concurrency slots taken on behalf of a racing group until each
// started entry takes over releasing its own.
type raceSlots struct {
	sem  chan struct{}
	held int
}

// handOff transfers one slot to an entry that started successfully; that call
// releases it when it finishes.
func (r *raceSlots) handOff() {
	if r.held > 0 {
		r.held--
	}
}

// releaseUnused returns slots no entry will claim, after an early failure.
func (r *raceSlots) releaseUnused() {
	for ; r.held > 0; r.held-- {
		<-r.sem
	}
}

// startAsyncLLMRace launches every invocation at once and returns a group whose
// finished channel yields calls in completion order.
func (s *schedulerExecutionState) startAsyncLLMRace(apiName string, invocations []schedulerLLMInvocation) (*asyncLLMRace, error) {
	slots, err := s.reserveRaceSlots(apiName, len(invocations))
	if err != nil {
		return nil, err
	}
	// Each started entry takes over releasing its own slot; only slots left
	// unused after an early failure are handed back here.
	defer slots.releaseUnused()

	raceCtx, cancel := context.WithCancel(s.asyncCtx)
	// Settling cancels the losers promptly; registering also covers a group the
	// script abandons, which never settles.
	s.registerAsyncCancel(cancel)
	race := &asyncLLMRace{
		finished: make(chan *asyncLLMCall, len(invocations)),
		byCall:   make(map[*asyncLLMCall]schedulerLLMInvocation, len(invocations)),
		total:    len(invocations),
		cancel:   cancel,
	}
	for _, invocation := range invocations {
		// byCall is written here and read only from the thenable, which cannot
		// run until this binding call has returned, so the map is complete
		// before any read. The goroutines report through finished and never
		// touch it.
		call, err := s.startAsyncLLM(raceCtx, apiName, invocation, true, race.finished)
		if err != nil {
			// This entry never took its slot; the deferred release hands back
			// every slot no entry claimed. Entries already started are
			// cancelled at teardown through the registered cancel.
			return nil, err
		}
		slots.handOff()
		race.byCall[call] = invocation
	}
	return race, nil
}

// parseSchedulerLLMInvocation decodes the argument list shared by every
// scheduler.llm entry point, so they cannot drift apart in how they read
// prompts, options, or schemas.
func parseSchedulerLLMInvocation(
	jsctx *qjs.Context,
	state *schedulerExecutionState,
	args []*qjs.Value,
	apiName string,
) (schedulerLLMInvocation, error) {
	if len(args) == 0 {
		return schedulerLLMInvocation{}, fmt.Errorf("%s requires a prompt", apiName)
	}
	prompt := strings.TrimSpace(args[0].String())
	if prompt == "" {
		return schedulerLLMInvocation{}, fmt.Errorf("%s requires a non-empty prompt", apiName)
	}
	options, err := parseSchedulerLLMRequest(args, state)
	if err != nil {
		return schedulerLLMInvocation{}, err
	}
	outputSchema, outputSchemaValue, err := parseSchedulerOutputSchema(jsctx, state.jsonEncoder, args, apiName)
	if err != nil {
		return schedulerLLMInvocation{}, err
	}
	options.OutputSchema = outputSchema
	return schedulerLLMInvocation{prompt: prompt, options: options, outputSchemaValue: outputSchemaValue}, nil
}

// parseSchedulerLLMRaceList decodes the array scheduler.llm.race and
// scheduler.llm.any accept. An entry is either a prompt string or an object
// carrying a prompt property alongside the usual scheduler.llm options, so
// callers can race the same prompt across models.
func parseSchedulerLLMRaceList(
	jsctx *qjs.Context,
	state *schedulerExecutionState,
	args []*qjs.Value,
	apiName string,
) ([]schedulerLLMInvocation, error) {
	if len(args) == 0 || args[0] == nil || !args[0].IsArray() {
		return nil, fmt.Errorf("%s requires an array of prompts", apiName)
	}
	list := args[0]
	length := list.Len()
	if length == 0 {
		return nil, fmt.Errorf("%s requires a non-empty array", apiName)
	}
	invocations := make([]schedulerLLMInvocation, 0, length)
	for index := int64(0); index < length; index++ {
		entry := list.GetPropertyIndex(index)
		var entryArgs []*qjs.Value
		switch {
		case entry.IsString():
			entryArgs = []*qjs.Value{entry}
		case entry.IsObject() && !entry.IsArray():
			prompt := entry.GetPropertyStr("prompt")
			if !prompt.IsString() {
				return nil, fmt.Errorf("%s entry %d requires a prompt", apiName, index)
			}
			// The entry doubles as the options object, so model and
			// outputSchema are read exactly as scheduler.llm reads them.
			entryArgs = []*qjs.Value{prompt, entry}
		default:
			// Without this an entry like null, 42, or an array would be
			// stringified into a prompt and billed as a real call.
			return nil, fmt.Errorf("%s entry %d must be a prompt string or an object with a prompt", apiName, index)
		}
		invocation, err := parseSchedulerLLMInvocation(jsctx, state, entryArgs, apiName)
		if err != nil {
			return nil, err
		}
		invocations = append(invocations, invocation)
	}
	return invocations, nil
}

// encodeSchedulerLLMResult converts a host result into the JS value every
// scheduler.llm entry point hands back, applying outputSchema parsing and
// validation identically.
func encodeSchedulerLLMResult(
	jsctx *qjs.Context,
	response domain.SchedulerLLMResult,
	options domain.SchedulerLLMRequest,
	outputSchemaValue *qjs.Value,
) (*qjs.Value, error) {
	hasSchema := strings.TrimSpace(options.OutputSchema) != ""
	if hasSchema {
		jsonValue, err := schedulerJSONResult(response.Text, options.OutputSchema, "llm text")
		if err != nil {
			return nil, err
		}
		response.JSON = jsonValue
	}
	data, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode scheduler.llm response: %w", err)
	}
	value, err := payloadValueFromJSON(jsctx, string(data))
	if err != nil {
		return nil, fmt.Errorf("decode scheduler.llm response: %w", err)
	}
	if hasSchema {
		if err := validateSchedulerJSONWithSchema(jsctx, outputSchemaValue, value, "llm"); err != nil {
			return nil, err
		}
	}
	return value, nil
}

// newAsyncLLMRacePromise resolves from a racing group in completion order.
// requireSuccess selects Promise.any semantics (skip rejections, reject only
// once every call has failed) over Promise.race semantics (adopt the first
// settled call, successful or not). Neither is reachable through the standard
// combinators here, because their thenable jobs run in queue order and block.
func newAsyncLLMRacePromise(
	state *schedulerExecutionState,
	jsctx *qjs.Context,
	apiName string,
	race *asyncLLMRace,
	requireSuccess bool,
) (*qjs.Value, error) {
	ctx := state.ctx
	return adoptAsyncThenable(state, jsctx, apiName, func() (*qjs.Value, error) {
		winner, invocation, err := race.await(ctx, requireSuccess)
		// Once the group has settled the remaining entries are dead weight.
		// Cancelling before releasing keeps the window in which a loser can
		// still finish with a full body as short as possible.
		race.cancel()
		// Deferred in reverse order of execution: the winner is released first,
		// then whatever already reported, then a reaper picks up stragglers.
		defer race.reapLateEntries(state.ctx)
		// Deferred so the bodies are dropped on the failure paths too: race
		// settling with a rejection, and any exhausting every entry.
		defer race.releaseSettledResults()
		if err != nil {
			return nil, err
		}
		// await took the winner out of the finished channel, so release it here
		// rather than leaving the largest body of the group behind.
		defer race.release(winner)
		return encodeSchedulerLLMResult(jsctx, winner.result, invocation.options, invocation.outputSchemaValue)
	})
}

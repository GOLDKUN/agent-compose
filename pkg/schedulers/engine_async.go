package schedulers

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	domain "agent-compose/pkg/model"

	"github.com/fastschema/qjs"
)

// DefaultMaxAsyncLLMConcurrency bounds concurrent async LLM calls when a
// request does not ask for a specific limit.
const DefaultMaxAsyncLLMConcurrency = 8

// DefaultMaxAsyncAgentConcurrency bounds concurrent async agent runs. It is far
// lower than the LLM limit because every parallel agent is a fresh sandbox:
// sandbox creation has no ceiling of its own, and the SQLite pool each run
// writes through defaults to four connections.
const DefaultMaxAsyncAgentConcurrency = 3

// asyncDrainGrace bounds how long draining waits for host calls that have not
// noticed cancellation yet.
const asyncDrainGrace = 2 * time.Second

// maxOutstandingAsyncCalls bounds how many async host calls one execution may
// have started but not finished.
//
// The concurrency limits gate how many calls run at once, but every pending
// handle still costs a goroutine and its stack, which lives outside the QuickJS
// memory limit. Fanning out over a long array would otherwise turn script input
// straight into hundreds of megabytes of Go stacks. The ceiling is far above
// any sensible batch, so it reads as a guard rail rather than a tuning knob.
const maxOutstandingAsyncCalls = 4096

// promiseAdopter holds the Promise intrinsic captured before user code runs.
//
// The async bindings hand scripts a promise by letting Promise.resolve adopt a
// thenable. Reading that off the global at call time would let a script decide
// what every async binding returns, the same way a tampered JSON.stringify
// would change the engine's serialization -- which is why jsValueEncoder
// captures its intrinsic up front too.
type promiseAdopter struct {
	promiseObject *qjs.Value
	resolve       *qjs.Value
}

func newPromiseAdopter(jsctx *qjs.Context) (*promiseAdopter, error) {
	promiseObject := jsctx.Global().GetPropertyStr("Promise")
	if promiseObject == nil || !promiseObject.IsObject() {
		return nil, fmt.Errorf("initialize promise adopter: Promise is unavailable")
	}
	resolve := promiseObject.GetPropertyStr("resolve")
	if resolve == nil || !resolve.IsFunction() {
		return nil, fmt.Errorf("initialize promise adopter: Promise.resolve is unavailable")
	}
	return &promiseAdopter{promiseObject: promiseObject, resolve: resolve}, nil
}

// adopt turns a thenable into a genuine promise.
func (a *promiseAdopter) adopt(jsctx *qjs.Context, thenable *qjs.Value) (*qjs.Value, error) {
	return jsctx.Invoke(a.resolve, a.promiseObject, thenable)
}

// asyncCallSpec describes one async host call to start.
//
// slot gates the call on a concurrency limit; LLM calls and agent runs use
// separate ones because an agent run is a whole sandbox while an LLM call is
// one HTTP request. slotHeld says the caller already took the slot, which a
// racing group does so that every entry really starts at once. notify, when
// set, reports the call once it has settled, which lets a racing group observe
// completion order; it must be buffered for the whole group so reporting never
// blocks.
type asyncCallSpec[T any] struct {
	apiName  string
	slot     chan struct{}
	slotHeld bool
	notify   chan<- *asyncCall[T]
	work     func(ctx context.Context) (T, error)
}

// asyncCall holds one host call that is already running on its own goroutine.
// done is closed (never sent to) so that awaiting the same handle twice, or
// awaiting it after it settled, both observe the outcome without blocking.
type asyncCall[T any] struct {
	done   chan struct{}
	result T
	err    error
}

// resolveMaxAsyncConcurrency turns a requested limit into an effective one.
func resolveMaxAsyncConcurrency(requested, fallback int) int {
	if requested <= 0 {
		return fallback
	}
	return requested
}

// startAsyncHostCall runs work on its own goroutine and returns immediately, so
// a script can start several host calls before awaiting any of them.
//
// slot gates the call on a concurrency limit; LLM calls and agent runs use
// separate ones because an agent run is a whole sandbox while an LLM call is
// one HTTP request. slotHeld says the caller already took the slot, which a
// racing group does so that every entry really starts at once. notify, when set, reports the call once it has settled,
// which lets a racing group observe completion order; it must be buffered for
// the whole group so reporting never blocks. ctx is usually the execution's own,
// but a racing group passes a narrower one it can cancel once a winner is known.
func startAsyncHostCall[T any](
	ctx context.Context,
	state *schedulerExecutionState,
	spec asyncCallSpec[T],
) (*asyncCall[T], error) {
	apiName, slot, slotHeld, notify := spec.apiName, spec.slot, spec.slotHeld, spec.notify
	if state.dryRun {
		// A dry run only observes what the script would request, so there is
		// nothing to run in parallel. Inline keeps what the host records
		// deterministic: on a goroutine an unawaited call races teardown's
		// cancellation, and losing that race makes prompt capture report a
		// confusing "must call scheduler.agent exactly once".
		if slotHeld {
			// A racing group took this slot before starting the call. Running
			// inline means there is no goroutine to hand it back, so without
			// this the budget drains one group at a time.
			defer func() { <-slot }()
		}
		call := &asyncCall[T]{done: make(chan struct{})}
		call.result, call.err = spec.work(ctx)
		close(call.done)
		if notify != nil {
			notify <- call
		}
		return call, nil
	}
	if err := state.reserveAsyncCall(apiName); err != nil {
		return nil, err
	}
	call := &asyncCall[T]{done: make(chan struct{})}
	state.inflight.Add(1)
	go func() {
		defer state.inflight.Done()
		defer state.releaseAsyncCall()
		if notify != nil {
			// Registered before close(done) so it runs after it: the call is
			// fully settled by the time the group observes it.
			defer func() { notify <- call }()
		}
		defer close(call.done)
		// Registered after close(done) so it runs before it, leaving err set
		// for whoever awaits the handle. A synchronous binding survives a
		// panicking host because qjs turns a panic inside a proxied function
		// into a JS exception; on this goroutine an unrecovered one would take
		// the whole daemon down instead.
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			slog.Error("scheduler async host call panicked",
				"api", apiName, "panic", recovered, "stack", string(debug.Stack()))
			call.err = fmt.Errorf("%s panicked: %v", apiName, recovered)
		}()

		if slotHeld {
			// The caller took this slot before starting the call, so releasing
			// is all that is left to do.
			defer func() { <-slot }()
		} else {
			select {
			case slot <- struct{}{}:
				defer func() { <-slot }()
			case <-ctx.Done():
				call.err = context.Cause(ctx)
				return
			}
		}
		call.result, call.err = spec.work(ctx)
	}()
	return call, nil
}

// reserveAsyncCall admits one more outstanding async call, or reports that this
// execution has fanned out too far.
func (s *schedulerExecutionState) reserveAsyncCall(apiName string) error {
	if s.outstandingAsync.Add(1) > maxOutstandingAsyncCalls {
		s.outstandingAsync.Add(-1)
		return fmt.Errorf(
			"%s has %d calls outstanding, the most one execution may start; await the current batch before starting more",
			apiName, maxOutstandingAsyncCalls,
		)
	}
	return nil
}

func (s *schedulerExecutionState) releaseAsyncCall() {
	s.outstandingAsync.Add(-1)
}

// newAsyncPromise wraps an in-flight call in a real JS promise.
//
// The bridge is a thenable: QuickJS treats any object with a then method as
// awaitable, which lets the engine hand results back without owning an event
// loop -- qjs v0.0.6 cannot provide one, since its wasm build exports no
// JS_ExecutePendingJob for Go to pump pending jobs with. then joins the
// goroutine rather than starting it, so by the time Promise.all invokes it
// every handle in the batch is already running and the batch costs the slowest
// call rather than their sum.
//
// The thenable is then adopted by Promise.resolve so scripts receive a genuine
// promise. Handing back the bare thenable would expose an object with only a
// then method: .catch and .finally would be missing.
//
// Adoption has a cost worth knowing about: Promise.resolve enqueues the
// thenable job at creation, and the queue is drained in order with every job
// blocking, so a handle created first has to settle before a later await can
// proceed -- even one the script abandoned. Returning a bare thenable would
// make adoption lazy and remove that coupling, at the price of hand-rolling
// catch and finally and giving up instanceof Promise. Documented under
// "并行调用" in examples/scheduler-script/README.md and pinned by
// TestSchedulerLLMAsyncAbandonedHandleDelaysLaterAwait.
func newAsyncPromise[T any](
	state *schedulerExecutionState,
	jsctx *qjs.Context,
	apiName string,
	call *asyncCall[T],
	encode func(T) (*qjs.Value, error),
) (*qjs.Value, error) {
	ctx := state.ctx
	return adoptAsyncThenable(state, jsctx, apiName, func() (*qjs.Value, error) {
		select {
		case <-call.done:
		case <-ctx.Done():
			// Awaiting must end with the run, even when the host call itself
			// has not noticed the cancellation.
			return nil, context.Cause(ctx)
		}
		if call.err != nil {
			// Returning an error from the thenable job rejects the promise, so
			// script-side try/catch and .catch() observe host failures normally.
			return nil, call.err
		}
		value, err := encode(call.result)
		// qjs v0.0.6 never unregisters the Go function backing a thenable, so
		// this closure -- and everything it captures -- lives until the runtime
		// closes. Dropping the payload once it has been converted to a JS value
		// keeps a run that makes thousands of sequential calls from retaining
		// every response body. The goroutine has finished by now, so nothing
		// else is writing it, and the promise memoizes its value, so a repeat
		// await never re-encodes.
		var released T
		call.result = released
		return value, err
	})
}

// adoptAsyncThenable builds the thenable that settles from settle and hands
// back the promise that adopts it.
func adoptAsyncThenable(
	state *schedulerExecutionState,
	jsctx *qjs.Context,
	apiName string,
	settle func() (*qjs.Value, error),
) (*qjs.Value, error) {
	handle := jsctx.NewObject()
	handle.SetPropertyStr("then", jsctx.Function(func(inner *qjs.This) (*qjs.Value, error) {
		value, err := settle()
		if err != nil {
			return nil, err
		}
		args := inner.Args()
		if len(args) == 0 || !args[0].IsFunction() {
			// The promise resolution procedure always supplies both callbacks,
			// so this is unreachable through the returned promise. Reject
			// rather than return a value: a then that settles neither callback
			// leaves the promise pending forever, which would surface as a run
			// that silently hangs until its deadline.
			return nil, fmt.Errorf("%s handle was resolved without a fulfillment callback", apiName)
		}
		return jsctx.Invoke(args[0], jsctx.Global(), value)
	}))

	promise, err := state.promises.adopt(jsctx, handle)
	if err != nil {
		return nil, fmt.Errorf("adopt %s handle as a promise: %w", apiName, err)
	}
	return promise, nil
}

// registerAsyncCancel records work to stop once the script has finished.
func (s *schedulerExecutionState) registerAsyncCancel(cancel context.CancelFunc) {
	s.asyncCancels = append(s.asyncCancels, cancel)
}

// drainInflight ends every async host call this execution started and waits for
// the goroutines to finish, so nothing is recorded against a run that has
// already been torn down.
//
// Draining runs only after the script has returned, so anything still in flight
// is abandoned by definition -- a handle the script awaited would have settled
// before reaching here. Cancelling rather than waiting matters because leaveRun
// is deferred until Execute returns: a fire-and-forget call that runs for
// minutes would hold the scheduler's run slot for just as long, and under
// concurrency_policy: skip every trigger arriving meanwhile is dropped.
//
// The wait is bounded whether or not the run itself was cancelled. A host that
// ignores cancellation is abandoned instead, which can let a late call record
// an event after teardown; that is the lesser failure, since waiting on it
// would block Execute forever and wedge the scheduler.
func (s *schedulerExecutionState) drainInflight() {
	for _, cancel := range s.asyncCancels {
		cancel()
	}

	done := make(chan struct{})
	go func() {
		s.inflight.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(asyncDrainGrace):
	}
}

// Scheduler env names that override the async concurrency budgets.
const (
	envMaxAsyncLLMConcurrency   = "LLM_MAX_CONCURRENCY"
	envMaxAsyncAgentConcurrency = "AGENT_MAX_CONCURRENCY"
)

// AsyncConcurrencyFromSchedulerEnv reads the async concurrency budgets a
// scheduler declares through its env items. A missing, unparsable, or
// non-positive value yields zero, which leaves the engine on its defaults --
// operators tune these, so a typo must not silently serialise or unleash a
// scheduler.
func AsyncConcurrencyFromSchedulerEnv(items []domain.SandboxEnvVar) (llm int, agent int) {
	return positiveEnvInt(items, envMaxAsyncLLMConcurrency), positiveEnvInt(items, envMaxAsyncAgentConcurrency)
}

func positiveEnvInt(items []domain.SandboxEnvVar, name string) int {
	for _, item := range items {
		if !strings.EqualFold(strings.TrimSpace(item.Name), name) {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(item.Value))
		if err != nil || value <= 0 {
			return 0
		}
		return value
	}
	return 0
}

package schedulers

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/fastschema/qjs"
)

// maxSleepMilliseconds is the largest millisecond value that still fits in a
// time.Duration once scaled to nanoseconds.
const maxSleepMilliseconds = int64(math.MaxInt64) / int64(time.Millisecond)

// asyncSleepCall is one pending scheduler.sleep.async delay.
type asyncSleepCall = asyncCall[struct{}]

// startAsyncSleep waits out d on its own goroutine so a script can overlap a
// delay with other async work.
//
// Unlike a host call a sleep takes no concurrency slot and is not tracked for
// draining. It reaches neither the host nor the database, so there is no result
// that could be recorded against a finished run -- the reason draining exists.
// Joining one would instead let a script stall its own run long after main()
// returned, simply by never awaiting the handle.
func (s *schedulerExecutionState) startAsyncSleep(d time.Duration) (*asyncSleepCall, error) {
	// A sleep costs a goroutine like any other handle, so it is admitted
	// against the same ceiling even though it is not drained.
	if err := s.reserveAsyncCall("scheduler.sleep.async"); err != nil {
		return nil, err
	}
	call := &asyncSleepCall{done: make(chan struct{})}
	go func() {
		defer s.releaseAsyncCall()
		defer close(call.done)
		call.err = sleepWithContext(s.asyncCtx, d)
	}()
	return call, nil
}

// sleepWithContext waits out d, returning early if the run is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// newAsyncSleepPromise adopts a pending sleep as a promise resolving to
// undefined, so it composes with Promise.all like any other async handle.
func newAsyncSleepPromise(state *schedulerExecutionState, jsctx *qjs.Context, call *asyncSleepCall) (*qjs.Value, error) {
	return newAsyncPromise(state, jsctx, "scheduler.sleep.async", call, func(struct{}) (*qjs.Value, error) {
		return jsctx.NewUndefined(), nil
	})
}

// parseSchedulerSleepDuration reads the millisecond argument shared by
// scheduler.sleep and scheduler.sleep.async.
func parseSchedulerSleepDuration(args []*qjs.Value, apiName string) (time.Duration, error) {
	if len(args) == 0 || args[0] == nil || !args[0].IsNumber() {
		return 0, fmt.Errorf("%s requires a duration in milliseconds", apiName)
	}
	ms := args[0].Int64()
	if ms <= 0 {
		return 0, fmt.Errorf("%s requires a positive duration", apiName)
	}
	// time.Duration counts nanoseconds, so the millisecond value overflows
	// int64 long before the number itself does. Left unchecked the product
	// wraps, turning an absurd request into an instant no-op or an arbitrary
	// wait instead of an error.
	if ms > maxSleepMilliseconds {
		return 0, fmt.Errorf("%s duration %d ms is out of range (max %d)", apiName, ms, maxSleepMilliseconds)
	}
	return time.Duration(ms) * time.Millisecond, nil
}

// skipsDelays reports whether this execution should return from a sleep at once.
// Validation collects triggers from the script's top level, and a dry run only
// observes the requests a script would make; in both the script is not doing
// real work, so its pacing must not stall the caller.
func (s *schedulerExecutionState) skipsDelays() bool {
	return s.host == nil || s.dryRun
}

package schedulers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	domain "agent-compose/pkg/model"

	"github.com/fastschema/qjs"
	"github.com/samber/do/v2"
)

type SchedulerHost interface {
	Log(ctx context.Context, message string, payload any) error
	PublishEvent(ctx context.Context, topic string, payloadJSON string) (domain.TopicEventRecord, error)
	Agent(ctx context.Context, prompt string, request domain.SchedulerAgentRequest) (domain.SchedulerAgentResult, error)
	Command(ctx context.Context, request domain.SchedulerCommandRequest) (domain.SchedulerCommandResult, error)
	LLM(ctx context.Context, prompt string, request domain.SchedulerLLMRequest) (domain.SchedulerLLMResult, error)
	StateGet(ctx context.Context, key string) (string, bool, error)
	StateSet(ctx context.Context, key, valueJSON string) error
	StateDelete(ctx context.Context, key string) error
	CallSandboxRPC(ctx context.Context, method, requestJSON string) (string, error)
}

type SchedulerValidationResult struct {
	Triggers []domain.SchedulerTrigger
	Warnings []string
}

type SchedulerExecutionRequest struct {
	Runtime     string
	Script      string
	Trigger     *domain.SchedulerTrigger
	PayloadJSON string

	// MaxAsyncLLMConcurrency caps concurrent scheduler.llm.async calls.
	// Zero selects DefaultMaxAsyncLLMConcurrency.
	MaxAsyncLLMConcurrency int
	// MaxAsyncAgentConcurrency caps concurrent scheduler.agent.async runs.
	// Zero selects DefaultMaxAsyncAgentConcurrency.
	MaxAsyncAgentConcurrency int

	// DryRun marks an execution that runs the script only to observe what it
	// would request, such as resolving a manual trigger's prompt. Delays are
	// skipped so a script's own pacing cannot stall the caller.
	DryRun bool
}

type SchedulerExecutionResult struct {
	Triggers   []domain.SchedulerTrigger
	Warnings   []string
	ResultJSON string
}

type SchedulerEngine interface {
	Validate(ctx context.Context, runtime, script string) (SchedulerValidationResult, error)
	Execute(ctx context.Context, request SchedulerExecutionRequest, host SchedulerHost) (SchedulerExecutionResult, error)
}

type QJSSchedulerEngine struct{}

type schedulerRegistration struct {
	trigger  domain.SchedulerTrigger
	callback *qjs.Value
	order    int
}

type schedulerExecutionState struct {
	ctx context.Context
	// asyncCtx scopes work started by the async bindings. Draining cancels it,
	// which ends anything the script abandoned instead of waiting it out.
	asyncCtx      context.Context
	host          SchedulerHost
	jsonEncoder   *jsValueEncoder
	promises      *promiseAdopter
	registrations []schedulerRegistration
	seenIDs       map[string]struct{}
	warnings      []string
	warningSet    map[string]struct{}
	dryRun        bool

	// inflight tracks host calls started by the async bindings. The run must
	// not finish while any of them is still running, or the host would keep
	// recording events against an execution that is already torn down.
	inflight sync.WaitGroup
	// llmSem and agentSem cap concurrent async host calls. A script can fan
	// out over an arbitrarily long array, and neither sandbox creation nor the
	// SQLite connection pool has a ceiling of its own, so these are safety
	// valves rather than tuning knobs. Agents get their own, much smaller
	// budget because each parallel agent run is a fresh sandbox.
	llmSem   chan struct{}
	agentSem chan struct{}
	// asyncCancels stops work that is only useful while the script is still
	// running, such as the losing entries of a racing group. Only the JS thread
	// appends to and reads this, so it needs no lock.
	asyncCancels []context.CancelFunc
	// outstandingAsync counts async calls started but not yet settled. The JS
	// thread raises it and the goroutines lower it, so it is atomic.
	outstandingAsync atomic.Int64
}

func NewSchedulerEngine(do.Injector) (SchedulerEngine, error) {
	return &QJSSchedulerEngine{}, nil
}

func (e *QJSSchedulerEngine) Validate(ctx context.Context, runtime, script string) (SchedulerValidationResult, error) {
	result, err := e.execute(ctx, SchedulerExecutionRequest{Runtime: runtime, Script: script}, nil, true)
	if err != nil {
		return SchedulerValidationResult{}, err
	}
	return SchedulerValidationResult{Triggers: result.Triggers, Warnings: result.Warnings}, nil
}

func (e *QJSSchedulerEngine) Execute(ctx context.Context, request SchedulerExecutionRequest, host SchedulerHost) (SchedulerExecutionResult, error) {
	return e.execute(ctx, request, host, false)
}

func (e *QJSSchedulerEngine) executeRuntime(ctx context.Context, request SchedulerExecutionRequest, host SchedulerHost, validateOnly bool) (SchedulerExecutionResult, error) {
	runtimeName, err := NormalizeRuntime(request.Runtime)
	if err != nil {
		return SchedulerExecutionResult{}, err
	}
	if runtimeName != domain.SchedulerRuntimeScheduler {
		return SchedulerExecutionResult{}, fmt.Errorf("unsupported scheduler runtime %q", runtimeName)
	}
	if strings.TrimSpace(request.Script) == "" {
		return SchedulerExecutionResult{}, fmt.Errorf("scheduler script is required")
	}

	rt, err := qjs.New(qjs.Option{
		Context:            ctx,
		CloseOnContextDone: true,
		MemoryLimit:        64 << 20,
		MaxExecutionTime:   schedulerEngineMaxExecutionTime(ctx),
	})
	if err != nil {
		return SchedulerExecutionResult{}, fmt.Errorf("create qjs runtime: %w", err)
	}
	defer closeQJSSchedulerRuntime(ctx, rt)

	jsctx := rt.Context()
	jsonEncoder, err := newJSValueEncoder(jsctx)
	if err != nil {
		return SchedulerExecutionResult{}, err
	}
	promises, err := newPromiseAdopter(jsctx)
	if err != nil {
		return SchedulerExecutionResult{}, err
	}
	asyncCtx, cancelAsync := context.WithCancel(ctx)
	state := &schedulerExecutionState{
		ctx:           ctx,
		asyncCtx:      asyncCtx,
		host:          host,
		jsonEncoder:   jsonEncoder,
		promises:      promises,
		registrations: make([]schedulerRegistration, 0),
		seenIDs:       make(map[string]struct{}),
		warningSet:    make(map[string]struct{}),
		dryRun:        request.DryRun,
		llmSem:        make(chan struct{}, resolveMaxAsyncConcurrency(request.MaxAsyncLLMConcurrency, DefaultMaxAsyncLLMConcurrency)),
		agentSem:      make(chan struct{}, resolveMaxAsyncConcurrency(request.MaxAsyncAgentConcurrency, DefaultMaxAsyncAgentConcurrency)),
	}
	state.registerAsyncCancel(cancelAsync)
	defer state.freeCallbacks()
	// Registered after freeCallbacks so it runs first: in-flight goroutines are
	// joined before any JS value is released and before the runtime is closed.
	defer state.drainInflight()

	if _, err = e.installRuntime(jsctx, state); err != nil {
		return SchedulerExecutionResult{}, err
	}

	evalResult, err := jsctx.Eval("scheduler.js", qjs.Code(request.Script), qjs.FlagAsync())
	if err != nil {
		return SchedulerExecutionResult{}, fmt.Errorf("evaluate scheduler script: %w", err)
	}
	if evalResult != nil {
		if evalResult.IsPromise() {
			if _, err := evalResult.Await(); err != nil {
				return SchedulerExecutionResult{}, fmt.Errorf("await scheduler script: %w", err)
			}
		}
	}

	warnings := append([]string(nil), state.warnings...)
	if len(state.registrations) == 0 {
		mainFn := jsctx.Global().GetPropertyStr("main")
		hasMain := mainFn.IsFunction()
		if !hasMain {
			warnings = append(warnings, "script does not register any trigger and does not define main()")
		}
	}

	result := SchedulerExecutionResult{
		Triggers: state.triggers(),
		Warnings: warnings,
	}
	if validateOnly {
		return result, nil
	}
	if host == nil {
		return SchedulerExecutionResult{}, fmt.Errorf("scheduler host is required for execution")
	}

	resultJSON, err := e.runRequestedHandler(ctx, jsctx, state, request)
	if err != nil {
		return SchedulerExecutionResult{}, err
	}
	result.ResultJSON = resultJSON
	result.Warnings = append([]string(nil), state.warnings...)
	return result, nil
}

// runRequestedHandler invokes the trigger handler requested by request,
// awaiting it if it returns a promise, and returns its JSON result if any.
func (e *QJSSchedulerEngine) runRequestedHandler(ctx context.Context, jsctx *qjs.Context, state *schedulerExecutionState, request SchedulerExecutionRequest) (string, error) {
	payloadValue, err := payloadValueFromJSON(jsctx, request.PayloadJSON)
	if err != nil {
		return "", err
	}

	executed, err := e.executeRequestedHandler(jsctx, state, request.Trigger, payloadValue)
	if err != nil {
		return "", err
	}
	resultJSON := ""
	if executed != nil {
		if executed.IsPromise() {
			awaited, err := executed.Await()
			if err != nil {
				return "", fmt.Errorf("await scheduler handler: %w", err)
			}
			executed = awaited
		}
		if jsonResult, ok, err := schedulerResultJSON(state.jsonEncoder, executed); err != nil {
			return "", err
		} else if ok {
			resultJSON = jsonResult
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return resultJSON, nil
}

func schedulerEngineMaxExecutionTime(ctx context.Context) int {
	return EngineMaxExecutionTime(ctx)
}

func EngineMaxExecutionTime(ctx context.Context) int {
	const defaultMaxExecutionTimeMs = int((60 * time.Minute) / time.Millisecond)
	deadline, ok := ctx.Deadline()
	if !ok {
		return defaultMaxExecutionTimeMs
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 1
	}
	remainingMs := int(remaining / time.Millisecond)
	if remainingMs < 1 {
		return 1
	}
	return remainingMs
}

func (e *QJSSchedulerEngine) executeRequestedHandler(jsctx *qjs.Context, state *schedulerExecutionState, trigger *domain.SchedulerTrigger, payload *qjs.Value) (*qjs.Value, error) {
	global := jsctx.Global()
	if trigger != nil && strings.TrimSpace(trigger.ID) != "" {
		for _, registration := range state.registrations {
			if registration.trigger.ID != strings.TrimSpace(trigger.ID) {
				continue
			}
			return jsctx.Invoke(registration.callback, global, payload)
		}
		return nil, fmt.Errorf("scheduler trigger %s not found in script", strings.TrimSpace(trigger.ID))
	}

	mainFn := global.GetPropertyStr("main")
	if mainFn.IsFunction() {
		return jsctx.Invoke(mainFn, global, payload)
	}

	if len(state.registrations) == 1 {
		return jsctx.Invoke(state.registrations[0].callback, global, payload)
	}
	if len(state.registrations) > 1 {
		return nil, fmt.Errorf("scheduler defines multiple triggers; choose a trigger explicitly or define main()")
	}
	return jsctx.NewUndefined(), nil
}

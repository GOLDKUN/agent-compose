package schedulers

import (
	"fmt"
	"strings"

	domain "github.com/chaitin/agent-compose/pkg/model"

	"github.com/fastschema/qjs"
)

func (e *QJSSchedulerEngine) installRuntime(jsctx *qjs.Context, state *schedulerExecutionState) (*qjs.Value, error) {
	schedulerObj := jsctx.NewObject()
	global := jsctx.Global()
	global.SetPropertyStr("scheduler", schedulerObj)
	if err := installSchedulerSchemaBuilder(jsctx); err != nil {
		return nil, err
	}

	installTimerBindings(jsctx, state, global, schedulerObj)
	installEventBindings(jsctx, state, schedulerObj)
	installLogBindings(jsctx, state, schedulerObj)
	installAgentBinding(jsctx, state, schedulerObj)
	installCommandBindings(jsctx, state, schedulerObj)
	installSleepBindings(jsctx, state, schedulerObj)
	installStateBindings(jsctx, state, schedulerObj)
	installSandboxBindings(jsctx, state, schedulerObj)

	runtimeObj := jsctx.NewObject()
	runtimeObj.SetPropertyStr("name", jsctx.NewString("scheduler"))
	schedulerObj.SetPropertyStr("runtime", runtimeObj)
	return schedulerObj, nil
}

// installTimerBindings registers setInterval/setTimeout (and their clear*
// counterparts) on both the JS global scope and the scheduler object.
func installTimerBindings(jsctx *qjs.Context, state *schedulerExecutionState, global, schedulerObj *qjs.Value) {
	registerTimer := func(kind string, call *qjs.This) (*qjs.Value, error) {
		var (
			id       string
			delayMs  int64
			callback *qjs.Value
			autoID   bool
			err      error
			specJSON string
		)
		switch kind {
		case domain.SchedulerTriggerKindInterval:
			id, delayMs, callback, autoID, err = parseIntervalRegistration(call.Args())
			specJSON = fmt.Sprintf(`{"kind":"interval","intervalMs":%d}`, delayMs)
		case domain.SchedulerTriggerKindTimeout:
			id, delayMs, callback, autoID, err = parseTimeoutRegistration(call.Args())
			specJSON = fmt.Sprintf(`{"kind":"timeout","delayMs":%d}`, delayMs)
		default:
			return nil, fmt.Errorf("unsupported timer trigger kind %q", kind)
		}
		if err != nil {
			return nil, err
		}
		if id == "" {
			id = TriggerStableID(kind, "", delayMs, callback.String(), len(state.registrations))
			autoID = true
		}
		trigger := domain.SchedulerTrigger{
			ID:         id,
			Kind:       kind,
			IntervalMs: delayMs,
			Enabled:    true,
			AutoID:     autoID,
			SpecJSON:   specJSON,
		}
		if err := state.register(trigger, callback); err != nil {
			return nil, err
		}
		return jsctx.NewString(id), nil
	}

	setIntervalFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		return registerTimer(domain.SchedulerTriggerKindInterval, call)
	})
	global.SetPropertyStr("setInterval", setIntervalFn)
	schedulerObj.SetPropertyStr("setInterval", setIntervalFn.Clone())
	schedulerObj.SetPropertyStr("interval", setIntervalFn.Clone())

	setTimeoutFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		return registerTimer(domain.SchedulerTriggerKindTimeout, call)
	})
	global.SetPropertyStr("setTimeout", setTimeoutFn)
	schedulerObj.SetPropertyStr("setTimeout", setTimeoutFn.Clone())
	schedulerObj.SetPropertyStr("timeout", setTimeoutFn.Clone())

	clearIntervalFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if len(call.Args()) > 0 {
			state.unregister(triggerHandle(call.Args()[0]), domain.SchedulerTriggerKindInterval)
		}
		return jsctx.NewUndefined(), nil
	})
	global.SetPropertyStr("clearInterval", clearIntervalFn)
	schedulerObj.SetPropertyStr("clearInterval", clearIntervalFn.Clone())

	clearTimeoutFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if len(call.Args()) > 0 {
			state.unregister(triggerHandle(call.Args()[0]), domain.SchedulerTriggerKindTimeout)
		}
		return jsctx.NewUndefined(), nil
	})
	global.SetPropertyStr("clearTimeout", clearTimeoutFn)
	schedulerObj.SetPropertyStr("clearTimeout", clearTimeoutFn.Clone())
}

// installEventBindings registers scheduler.on/addEventListener and
// scheduler.cron/schedule.
func installEventBindings(jsctx *qjs.Context, state *schedulerExecutionState, schedulerObj *qjs.Value) {
	onFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		topic, id, callback, autoID, err := parseEventRegistration(call.Args())
		if err != nil {
			return nil, err
		}
		if id == "" {
			id = TriggerStableID(domain.SchedulerTriggerKindEvent, topic, 0, callback.String(), len(state.registrations))
			autoID = true
		}
		trigger := domain.SchedulerTrigger{
			ID:       id,
			Kind:     domain.SchedulerTriggerKindEvent,
			Topic:    topic,
			Enabled:  true,
			AutoID:   autoID,
			SpecJSON: fmt.Sprintf(`{"kind":"event","topic":%q}`, topic),
		}
		if err := state.register(trigger, callback); err != nil {
			return nil, err
		}
		return jsctx.NewString(id), nil
	})
	schedulerObj.SetPropertyStr("on", onFn)
	schedulerObj.SetPropertyStr("addEventListener", onFn.Clone())

	cronFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		expr, id, callback, specJSON, autoID, err := parseCronRegistration(call.Args())
		if err != nil {
			return nil, err
		}
		if id == "" {
			id = TriggerStableID(domain.SchedulerTriggerKindCron, expr, 0, callback.String(), len(state.registrations))
			autoID = true
		}
		trigger := domain.SchedulerTrigger{
			ID:       id,
			Kind:     domain.SchedulerTriggerKindCron,
			Enabled:  true,
			AutoID:   autoID,
			SpecJSON: specJSON,
		}
		if err := state.register(trigger, callback); err != nil {
			return nil, err
		}
		return jsctx.NewString(id), nil
	})
	schedulerObj.SetPropertyStr("cron", cronFn)
	schedulerObj.SetPropertyStr("schedule", cronFn.Clone())
}

// installLogBindings registers scheduler.log and scheduler.event.publish.
func installLogBindings(jsctx *qjs.Context, state *schedulerExecutionState, schedulerObj *qjs.Value) {
	logFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if state.host == nil {
			return jsctx.NewUndefined(), nil
		}
		args := call.Args()
		if len(args) == 0 {
			return nil, fmt.Errorf("scheduler.log requires a message")
		}
		message := strings.TrimSpace(args[0].String())
		if message == "" {
			return nil, fmt.Errorf("scheduler.log requires a non-empty message")
		}
		var payload any
		if len(args) > 1 && args[1] != nil && !args[1].IsUndefined() && !args[1].IsNull() {
			value, err := qjs.ToGoValue[any](args[1])
			if err != nil {
				return nil, fmt.Errorf("decode scheduler.log payload: %w", err)
			}
			payload = value
		}
		if err := state.host.Log(state.ctx, message, payload); err != nil {
			return nil, err
		}
		return jsctx.NewUndefined(), nil
	})
	schedulerObj.SetPropertyStr("log", logFn)

	eventObj := jsctx.NewObject()
	eventPublishFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if state.host == nil {
			return nil, fmt.Errorf("scheduler.event.publish is unavailable during validation")
		}
		args := call.Args()
		if len(args) < 2 {
			return nil, fmt.Errorf("scheduler.event.publish requires a topic and payload")
		}
		topic := strings.TrimSpace(args[0].String())
		if topic == "" {
			return nil, fmt.Errorf("scheduler.event.publish requires a non-empty topic")
		}
		if args[1].IsUndefined() || args[1].IsNull() || !args[1].IsObject() || args[1].IsArray() {
			return nil, fmt.Errorf("scheduler.event.publish payload must be an object")
		}
		payloadJSON, err := state.jsonEncoder.Encode(args[1])
		if err != nil {
			return nil, fmt.Errorf("encode scheduler.event.publish payload: %w", err)
		}
		record, err := state.host.PublishEvent(state.ctx, topic, payloadJSON)
		if err != nil {
			return nil, err
		}
		responseJSON, err := marshalJSONCompact(map[string]any{
			"eventId":       record.ID,
			"sequence":      record.Sequence,
			"topic":         record.Topic,
			"correlationId": record.CorrelationID,
		})
		if err != nil {
			return nil, err
		}
		return payloadValueFromJSON(jsctx, responseJSON)
	})
	eventObj.SetPropertyStr("publish", eventPublishFn)
	schedulerObj.SetPropertyStr("event", eventObj)
}

// installAgentBinding registers scheduler.agent and scheduler.agent.async.
func installAgentBinding(jsctx *qjs.Context, state *schedulerExecutionState, schedulerObj *qjs.Value) {
	agentFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if state.host == nil {
			return nil, fmt.Errorf("scheduler.agent is unavailable during validation")
		}
		invocation, err := parseSchedulerAgentInvocation(jsctx, state, call.Args(), "scheduler.agent")
		if err != nil {
			return nil, err
		}
		response, err := state.host.Agent(state.ctx, invocation.prompt, invocation.options)
		if err != nil {
			return nil, err
		}
		return encodeSchedulerAgentResult(jsctx, response, invocation.options, invocation.outputSchemaValue)
	})
	// scheduler.agent.async starts the agent run and hands the script a
	// promise, so a batch built with Promise.all overlaps instead of running
	// serially. Each call is pinned to its own sandbox; see
	// requireFreshSandboxForAsyncAgent.
	agentFn.SetPropertyStr("async", jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if state.host == nil {
			return nil, fmt.Errorf("scheduler.agent.async is unavailable during validation")
		}
		invocation, err := parseSchedulerAgentInvocation(jsctx, state, call.Args(), "scheduler.agent.async")
		if err != nil {
			return nil, err
		}
		invocation.options, err = requireFreshSandboxForAsyncAgent(invocation.options, "scheduler.agent.async")
		if err != nil {
			return nil, err
		}
		pending, err := state.startAsyncAgent(invocation)
		if err != nil {
			return nil, err
		}
		return newAsyncPromise(state, jsctx, "scheduler.agent.async", pending,
			func(response domain.SchedulerAgentResult) (*qjs.Value, error) {
				return encodeSchedulerAgentResult(jsctx, response, invocation.options, invocation.outputSchemaValue)
			})
	}))
	schedulerObj.SetPropertyStr("agent", agentFn)
}

// installCommandBindings registers scheduler.exec, scheduler.shell, and
// scheduler.llm.
func installCommandBindings(jsctx *qjs.Context, state *schedulerExecutionState, schedulerObj *qjs.Value) {
	execFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if state.host == nil {
			return nil, fmt.Errorf("scheduler.exec is unavailable during validation")
		}
		request, err := parseSchedulerExecRequest(call.Args(), state)
		if err != nil {
			return nil, err
		}
		response, err := state.host.Command(state.ctx, request)
		if err != nil {
			return nil, err
		}
		return schedulerCommandResultValue(jsctx, "scheduler.exec", response)
	})
	schedulerObj.SetPropertyStr("exec", execFn)

	shellFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if state.host == nil {
			return nil, fmt.Errorf("scheduler.shell is unavailable during validation")
		}
		request, err := parseSchedulerShellRequest(call.Args(), state)
		if err != nil {
			return nil, err
		}
		response, err := state.host.Command(state.ctx, request)
		if err != nil {
			return nil, err
		}
		return schedulerCommandResultValue(jsctx, "scheduler.shell", response)
	})
	schedulerObj.SetPropertyStr("shell", shellFn)

	llmFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if state.host == nil {
			return nil, fmt.Errorf("scheduler.llm is unavailable during validation")
		}
		invocation, err := parseSchedulerLLMInvocation(jsctx, state, call.Args(), "scheduler.llm")
		if err != nil {
			return nil, err
		}
		response, err := state.host.LLM(state.ctx, invocation.prompt, invocation.options)
		if err != nil {
			return nil, err
		}
		return encodeSchedulerLLMResult(jsctx, response, invocation.options, invocation.outputSchemaValue)
	})
	// scheduler.llm.async starts the host call and hands the script a thenable,
	// so a batch built with Promise.all overlaps instead of running serially.
	llmFn.SetPropertyStr("async", jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if state.host == nil {
			return nil, fmt.Errorf("scheduler.llm.async is unavailable during validation")
		}
		invocation, err := parseSchedulerLLMInvocation(jsctx, state, call.Args(), "scheduler.llm.async")
		if err != nil {
			return nil, err
		}
		pending, err := state.startAsyncLLM(state.asyncCtx, "scheduler.llm.async", invocation, false, nil)
		if err != nil {
			return nil, err
		}
		return newAsyncPromise(state, jsctx, "scheduler.llm.async", pending,
			func(response domain.SchedulerLLMResult) (*qjs.Value, error) {
				return encodeSchedulerLLMResult(jsctx, response, invocation.options, invocation.outputSchemaValue)
			})
	}))
	// scheduler.llm.race settles with the call that finished first. The
	// standard Promise.race cannot: its thenable jobs run in queue order and
	// each blocks, so it always settles with the first array entry.
	installSchedulerLLMRaceBinding(jsctx, state, llmFn, "race", false)
	// scheduler.llm.any skips rejected calls and settles with the first one
	// that succeeded, rejecting only after every call has failed.
	installSchedulerLLMRaceBinding(jsctx, state, llmFn, "any", true)
	schedulerObj.SetPropertyStr("llm", llmFn)
}

// installSleepBindings registers scheduler.sleep and scheduler.sleep.async.
// The global setTimeout is a trigger registrar in this runtime, so the usual
// `await new Promise(r => setTimeout(r, ms))` idiom never settles; these are
// the supported way for a script to wait.
func installSleepBindings(jsctx *qjs.Context, state *schedulerExecutionState, schedulerObj *qjs.Value) {
	sleepFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		duration, err := parseSchedulerSleepDuration(call.Args(), "scheduler.sleep")
		if err != nil {
			return nil, err
		}
		// Neither validation nor a dry run should wait: validation evaluates the
		// script's top level to collect triggers, and a dry run only observes
		// what the script would request. Arguments are still checked; only the
		// wait is skipped.
		if state.skipsDelays() {
			return jsctx.NewUndefined(), nil
		}
		if err := sleepWithContext(state.ctx, duration); err != nil {
			return nil, err
		}
		return jsctx.NewUndefined(), nil
	})
	sleepFn.SetPropertyStr("async", jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		duration, err := parseSchedulerSleepDuration(call.Args(), "scheduler.sleep.async")
		if err != nil {
			return nil, err
		}
		if state.skipsDelays() {
			duration = 0
		}
		pending, err := state.startAsyncSleep(duration)
		if err != nil {
			return nil, err
		}
		return newAsyncSleepPromise(state, jsctx, pending)
	}))
	schedulerObj.SetPropertyStr("sleep", sleepFn)
}

// installSchedulerLLMRaceBinding registers one completion-order combinator on
// scheduler.llm under the given name.
func installSchedulerLLMRaceBinding(jsctx *qjs.Context, state *schedulerExecutionState, llmFn *qjs.Value, name string, requireSuccess bool) {
	apiName := "scheduler.llm." + name
	llmFn.SetPropertyStr(name, jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if state.host == nil {
			return nil, fmt.Errorf("%s is unavailable during validation", apiName)
		}
		invocations, err := parseSchedulerLLMRaceList(jsctx, state, call.Args(), apiName)
		if err != nil {
			return nil, err
		}
		race, err := state.startAsyncLLMRace(apiName, invocations)
		if err != nil {
			return nil, err
		}
		return newAsyncLLMRacePromise(state, jsctx, apiName, race, requireSuccess)
	}))
}

// installStateBindings registers scheduler.state.get/set/delete.
func installStateBindings(jsctx *qjs.Context, state *schedulerExecutionState, schedulerObj *qjs.Value) {
	stateObj := jsctx.NewObject()
	stateGetFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if state.host == nil {
			return jsctx.NewUndefined(), nil
		}
		args := call.Args()
		if len(args) == 0 {
			return nil, fmt.Errorf("scheduler.state.get requires a key")
		}
		key := strings.TrimSpace(args[0].String())
		if key == "" {
			return nil, fmt.Errorf("scheduler.state.get requires a non-empty key")
		}
		valueJSON, ok, err := state.host.StateGet(state.ctx, key)
		if err != nil {
			return nil, err
		}
		if !ok {
			return jsctx.NewUndefined(), nil
		}
		value, err := payloadValueFromJSON(jsctx, valueJSON)
		if err != nil {
			return nil, err
		}
		return value, nil
	})
	stateSetFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if state.host == nil {
			return jsctx.NewUndefined(), nil
		}
		args := call.Args()
		if len(args) < 2 {
			return nil, fmt.Errorf("scheduler.state.set requires a key and value")
		}
		key := strings.TrimSpace(args[0].String())
		if key == "" {
			return nil, fmt.Errorf("scheduler.state.set requires a non-empty key")
		}
		if args[1].IsUndefined() {
			if err := state.host.StateDelete(state.ctx, key); err != nil {
				return nil, err
			}
			return jsctx.NewUndefined(), nil
		}
		valueJSON, err := state.jsonEncoder.Encode(args[1])
		if err != nil {
			return nil, err
		}
		if err := state.host.StateSet(state.ctx, key, valueJSON); err != nil {
			return nil, err
		}
		return jsctx.NewUndefined(), nil
	})
	stateDeleteFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
		if state.host == nil {
			return jsctx.NewUndefined(), nil
		}
		args := call.Args()
		if len(args) == 0 {
			return nil, fmt.Errorf("scheduler.state.delete requires a key")
		}
		key := strings.TrimSpace(args[0].String())
		if key == "" {
			return nil, fmt.Errorf("scheduler.state.delete requires a non-empty key")
		}
		if err := state.host.StateDelete(state.ctx, key); err != nil {
			return nil, err
		}
		return jsctx.NewUndefined(), nil
	})
	stateObj.SetPropertyStr("get", stateGetFn)
	stateObj.SetPropertyStr("set", stateSetFn)
	stateObj.SetPropertyStr("delete", stateDeleteFn)
	schedulerObj.SetPropertyStr("state", stateObj)
}

// installSandboxBindings registers scheduler.sandbox.* RPC bindings.
func installSandboxBindings(jsctx *qjs.Context, state *schedulerExecutionState, schedulerObj *qjs.Value) {
	sandboxObj := jsctx.NewObject()
	for _, binding := range []struct {
		jsName     string
		methodName string
	}{
		{jsName: "createSandbox", methodName: "CreateSandbox"},
		{jsName: "resumeSandbox", methodName: "ResumeSandbox"},
		{jsName: "stopSandbox", methodName: "StopSandbox"},
		{jsName: "getSandbox", methodName: "GetSandbox"},
		{jsName: "listSandboxes", methodName: "ListSandboxes"},
		{jsName: "getSandboxProxy", methodName: "GetSandboxProxy"},
	} {
		jsNameCopy := binding.jsName
		methodNameCopy := binding.methodName
		apiNameCopy := "scheduler.sandbox." + jsNameCopy
		sandboxFn := jsctx.Function(func(call *qjs.This) (*qjs.Value, error) {
			if state.host == nil {
				return nil, fmt.Errorf("%s is unavailable during validation", apiNameCopy)
			}
			requestJSON, err := schedulerRPCRequestJSON(state.jsonEncoder, call.Args(), apiNameCopy)
			if err != nil {
				return nil, err
			}
			responseJSON, err := state.host.CallSandboxRPC(state.ctx, methodNameCopy, requestJSON)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", apiNameCopy, err)
			}
			response, err := payloadValueFromJSON(jsctx, responseJSON)
			if err != nil {
				return nil, fmt.Errorf("decode %s response: %w", apiNameCopy, err)
			}
			return response, nil
		})
		sandboxObj.SetPropertyStr(jsNameCopy, sandboxFn)
		sandboxObj.SetPropertyStr(upperFirstASCII(jsNameCopy), sandboxFn.Clone())
	}
	schedulerObj.SetPropertyStr("sandbox", sandboxObj)
}

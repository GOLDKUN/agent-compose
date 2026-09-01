package schedulers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	domain "github.com/chaitin/agent-compose/pkg/model"

	"github.com/fastschema/qjs"
)

// asyncAgentCall is one in-flight scheduler.agent.async call.
type asyncAgentCall = asyncCall[domain.SchedulerAgentResult]

// schedulerAgentInvocation is one parsed scheduler.agent call.
type schedulerAgentInvocation struct {
	prompt            string
	options           domain.SchedulerAgentRequest
	outputSchemaValue *qjs.Value
}

func (s *schedulerExecutionState) startAsyncAgent(invocation schedulerAgentInvocation) (*asyncAgentCall, error) {
	return startAsyncHostCall(s.asyncCtx, s, asyncCallSpec[domain.SchedulerAgentResult]{
		apiName: "scheduler.agent.async",
		slot:    s.agentSem,
		work: func(ctx context.Context) (domain.SchedulerAgentResult, error) {
			return s.host.Agent(ctx, invocation.prompt, invocation.options)
		},
	})
}

// parseSchedulerAgentInvocation decodes the argument list shared by
// scheduler.agent and scheduler.agent.async.
func parseSchedulerAgentInvocation(
	jsctx *qjs.Context,
	state *schedulerExecutionState,
	args []*qjs.Value,
	apiName string,
) (schedulerAgentInvocation, error) {
	if len(args) == 0 {
		return schedulerAgentInvocation{}, fmt.Errorf("%s requires a prompt", apiName)
	}
	prompt := strings.TrimSpace(args[0].String())
	if prompt == "" {
		return schedulerAgentInvocation{}, fmt.Errorf("%s requires a non-empty prompt", apiName)
	}
	options, err := parseSchedulerAgentRequest(args, state)
	if err != nil {
		return schedulerAgentInvocation{}, err
	}
	outputSchema, outputSchemaValue, err := parseSchedulerOutputSchema(jsctx, state.jsonEncoder, args, apiName)
	if err != nil {
		return schedulerAgentInvocation{}, err
	}
	options.OutputSchema = outputSchema
	return schedulerAgentInvocation{prompt: prompt, options: options, outputSchemaValue: outputSchemaValue}, nil
}

// requireFreshSandboxForAsyncAgent pins an async agent call to its own sandbox.
//
// Parallel agents cannot share one: concurrent runs would write the same
// workspace, and the classic agent path shuts its sandbox down when a call
// finishes, so the first to complete would pull it out from under the others.
// An unset policy therefore becomes "new", and an explicitly incompatible one
// is rejected rather than silently reinterpreted.
func requireFreshSandboxForAsyncAgent(options domain.SchedulerAgentRequest, apiName string) (domain.SchedulerAgentRequest, error) {
	requested := strings.TrimSpace(AgentSandboxPolicy(options))
	if requested == "" {
		options.SandboxPolicy = domain.SchedulerSandboxPolicyNew
		return options, nil
	}
	if NormalizeSandboxPolicy(requested) != domain.SchedulerSandboxPolicyNew {
		return domain.SchedulerAgentRequest{}, fmt.Errorf(
			"%s requires sandboxPolicy %q but received %q: parallel agents cannot share a sandbox",
			apiName, domain.SchedulerSandboxPolicyNew, requested,
		)
	}
	options.SandboxPolicy = domain.SchedulerSandboxPolicyNew
	return options, nil
}

// encodeSchedulerAgentResult converts a host result into the JS value both
// scheduler.agent and scheduler.agent.async hand back.
func encodeSchedulerAgentResult(
	jsctx *qjs.Context,
	response domain.SchedulerAgentResult,
	options domain.SchedulerAgentRequest,
	outputSchemaValue *qjs.Value,
) (*qjs.Value, error) {
	hasSchema := strings.TrimSpace(options.OutputSchema) != ""
	if hasSchema {
		jsonValue, err := schedulerJSONResult(firstNonEmpty(response.FinalText, response.Text, response.Output), options.OutputSchema, "agent finalText")
		if err != nil {
			return nil, err
		}
		response.JSON = jsonValue
	}
	data, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode scheduler.agent response: %w", err)
	}
	value, err := payloadValueFromJSON(jsctx, string(data))
	if err != nil {
		return nil, fmt.Errorf("decode scheduler.agent response: %w", err)
	}
	if hasSchema {
		if err := validateSchedulerJSONWithSchema(jsctx, outputSchemaValue, value, "agent"); err != nil {
			return nil, err
		}
	}
	return value, nil
}

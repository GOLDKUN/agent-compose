package schedulers

import (
	"encoding/json"
	"fmt"

	domain "agent-compose/pkg/model"

	"github.com/fastschema/qjs"
)

func parseSchedulerAgentRequest(args []*qjs.Value, state *schedulerExecutionState) (domain.SchedulerAgentRequest, error) {
	request := domain.SchedulerAgentRequest{}
	if len(args) < 2 || args[1] == nil || args[1].IsUndefined() || args[1].IsNull() {
		return request, nil
	}
	options, err := schedulerAgentOptionsWithoutSchema(state.jsonEncoder, args[1])
	if err != nil {
		return domain.SchedulerAgentRequest{}, fmt.Errorf("decode scheduler.agent options: %w", err)
	}
	request.Agent = normalizeAgentKind(schedulerStringOption(options, "agent"))
	request.SandboxPolicy = schedulerSandboxPolicyOption(options, state, "scheduler.agent")
	request.Timeout, err = schedulerDurationOption(options, "timeout", "agentTimeout", "agent_timeout")
	if err != nil {
		return domain.SchedulerAgentRequest{}, fmt.Errorf("decode scheduler.agent timeout: %w", err)
	}
	request.Title = schedulerStringOption(options, "title")
	request.Driver = schedulerStringOption(options, "driver")
	request.GuestImage = schedulerStringOption(options, "guestImage", "guest_image")
	request.PullPolicy = normalizeImagePullPolicy(schedulerStringOption(options, "pullPolicy", "pull_policy"))
	request.WorkspaceID = schedulerStringOption(options, "workspaceId", "workspace_id")
	request.JupyterEnabled = schedulerBoolOption(options, "jupyter")
	request.SandboxEnv, err = schedulerSandboxEnvOption(options, state, "scheduler.agent")
	if err != nil {
		return domain.SchedulerAgentRequest{}, err
	}
	request.Volumes, err = schedulerVolumeMountSpecsOption(options, "scheduler.agent")
	if err != nil {
		return domain.SchedulerAgentRequest{}, err
	}
	return request, nil
}

func schedulerAgentOptionsWithoutSchema(encoder *jsValueEncoder, value *qjs.Value) (map[string]any, error) {
	if value == nil || value.IsUndefined() || value.IsNull() {
		return map[string]any{}, nil
	}
	if !value.IsObject() || value.IsArray() {
		return qjs.ToGoValue[map[string]any](value)
	}
	rawJSON, err := encoder.Encode(value)
	if err != nil {
		return nil, err
	}
	var options map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &options); err != nil {
		return nil, err
	}
	delete(options, "outputSchema")
	delete(options, "schema")
	return options, nil
}

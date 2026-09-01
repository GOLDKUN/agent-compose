package schedulers

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	domain "github.com/chaitin/agent-compose/pkg/model"
	"github.com/chaitin/agent-compose/pkg/volumes"

	"github.com/fastschema/qjs"
)

func parseSchedulerExecRequest(args []*qjs.Value, state *schedulerExecutionState) (domain.SchedulerCommandRequest, error) {
	if len(args) != 1 || args[0] == nil || args[0].IsUndefined() || args[0].IsNull() || !args[0].IsObject() || args[0].IsArray() {
		return domain.SchedulerCommandRequest{}, fmt.Errorf("scheduler.exec requires a request object")
	}
	options, err := qjs.ToGoValue[map[string]any](args[0])
	if err != nil {
		return domain.SchedulerCommandRequest{}, fmt.Errorf("decode scheduler.exec request: %w", err)
	}
	request, err := schedulerCommandRequestFromOptions(options, "scheduler.exec", state)
	if err != nil {
		return domain.SchedulerCommandRequest{}, err
	}
	request.Mode = "exec"
	request.Command = schedulerStringOption(options, "command")
	if strings.TrimSpace(request.Command) == "" {
		return domain.SchedulerCommandRequest{}, fmt.Errorf("scheduler.exec requires a non-empty command")
	}
	request.Args, err = schedulerStringArrayOption(options, "args")
	if err != nil {
		return domain.SchedulerCommandRequest{}, fmt.Errorf("decode scheduler.exec args: %w", err)
	}
	return request, nil
}

func parseSchedulerShellRequest(args []*qjs.Value, state *schedulerExecutionState) (domain.SchedulerCommandRequest, error) {
	if len(args) == 0 || args[0] == nil || args[0].IsUndefined() || args[0].IsNull() {
		return domain.SchedulerCommandRequest{}, fmt.Errorf("scheduler.shell requires a script")
	}
	if len(args) > 2 {
		return domain.SchedulerCommandRequest{}, fmt.Errorf("scheduler.shell accepts a script and optional options object")
	}
	script := args[0].String()
	if strings.TrimSpace(script) == "" {
		return domain.SchedulerCommandRequest{}, fmt.Errorf("scheduler.shell requires a non-empty script")
	}
	options := map[string]any{}
	if len(args) > 1 && args[1] != nil && !args[1].IsUndefined() && !args[1].IsNull() {
		if !args[1].IsObject() || args[1].IsArray() {
			return domain.SchedulerCommandRequest{}, fmt.Errorf("scheduler.shell options must be an object")
		}
		decoded, err := qjs.ToGoValue[map[string]any](args[1])
		if err != nil {
			return domain.SchedulerCommandRequest{}, fmt.Errorf("decode scheduler.shell options: %w", err)
		}
		options = decoded
	}
	request, err := schedulerCommandRequestFromOptions(options, "scheduler.shell", state)
	if err != nil {
		return domain.SchedulerCommandRequest{}, err
	}
	request.Mode = "shell"
	request.Script = script
	return request, nil
}

func schedulerCommandRequestFromOptions(options map[string]any, apiName string, state *schedulerExecutionState) (domain.SchedulerCommandRequest, error) {
	var err error
	request := domain.SchedulerCommandRequest{
		Cwd:            schedulerStringOption(options, "cwd"),
		SandboxPolicy:  schedulerSandboxPolicyOption(options, state, apiName),
		Title:          schedulerStringOption(options, "title"),
		Driver:         schedulerStringOption(options, "driver"),
		GuestImage:     schedulerStringOption(options, "guestImage", "guest_image"),
		PullPolicy:     normalizeImagePullPolicy(schedulerStringOption(options, "pullPolicy", "pull_policy")),
		WorkspaceID:    schedulerStringOption(options, "workspaceId", "workspace_id"),
		JupyterEnabled: schedulerBoolOption(options, "jupyter"),
	}
	request.Env, err = schedulerStringMapOption(options, "env")
	if err != nil {
		return domain.SchedulerCommandRequest{}, fmt.Errorf("decode %s env: %w", apiName, err)
	}
	request.TimeoutMs, err = schedulerInt64Option(options, "timeoutMs", "timeout_ms")
	if err != nil {
		return domain.SchedulerCommandRequest{}, fmt.Errorf("decode %s timeoutMs: %w", apiName, err)
	}
	request.MaxOutputBytes, err = schedulerInt64Option(options, "maxOutputBytes", "max_output_bytes")
	if err != nil {
		return domain.SchedulerCommandRequest{}, fmt.Errorf("decode %s maxOutputBytes: %w", apiName, err)
	}
	request.SandboxEnv, err = schedulerSandboxEnvOption(options, state, apiName)
	if err != nil {
		return domain.SchedulerCommandRequest{}, fmt.Errorf("%s", strings.Replace(err.Error(), "scheduler.agent", apiName, 1))
	}
	request.Volumes, err = schedulerVolumeMountSpecsOption(options, apiName)
	if err != nil {
		return domain.SchedulerCommandRequest{}, err
	}
	return request, nil
}

func schedulerVolumeMountSpecsOption(options map[string]any, apiName string) ([]domain.VolumeMountSpec, error) {
	value, ok := options["volumes"]
	if !ok || value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("decode %s volumes: must be an array", apiName)
	}
	specs := make([]domain.VolumeMountSpec, 0, len(items))
	for i, item := range items {
		spec, err := schedulerVolumeMountSpec(item)
		if err != nil {
			return nil, fmt.Errorf("decode %s volumes[%d]: %w", apiName, i, err)
		}
		specs = append(specs, spec)
	}
	normalized, err := volumes.NormalizeMountSpecs(specs)
	if err != nil {
		return nil, fmt.Errorf("decode %s volumes: %w", apiName, err)
	}
	return normalized, nil
}

func schedulerVolumeMountSpec(value any) (domain.VolumeMountSpec, error) {
	switch typed := value.(type) {
	case string:
		return parseSchedulerVolumeMountShortSyntax(typed)
	case map[string]any:
		spec := domain.VolumeMountSpec{
			Type:     schedulerStringOption(typed, "type"),
			Source:   schedulerStringOption(typed, "source"),
			Target:   schedulerStringOption(typed, "target"),
			ReadOnly: schedulerBoolOption(typed, "readOnly", "read_only"),
		}
		if strings.TrimSpace(spec.Type) == "" {
			spec.Type = inferSchedulerVolumeMountType(spec.Source)
		}
		return spec, nil
	default:
		return domain.VolumeMountSpec{}, fmt.Errorf("must be a string or object")
	}
}

func parseSchedulerVolumeMountShortSyntax(raw string) (domain.VolumeMountSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return domain.VolumeMountSpec{}, fmt.Errorf("volume short syntax is required")
	}
	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return domain.VolumeMountSpec{}, fmt.Errorf("volume short syntax must be source:target[:ro]")
	}
	source := strings.TrimSpace(parts[0])
	target := strings.TrimSpace(parts[1])
	if source == "" || target == "" {
		return domain.VolumeMountSpec{}, fmt.Errorf("volume short syntax requires source and target")
	}
	readOnly := false
	if len(parts) == 3 {
		switch strings.ToLower(strings.TrimSpace(parts[2])) {
		case "", "rw":
		case "ro", "readonly", "read_only":
			readOnly = true
		default:
			return domain.VolumeMountSpec{}, fmt.Errorf("unsupported volume short syntax mode %q", parts[2])
		}
	}
	return domain.VolumeMountSpec{Type: inferSchedulerVolumeMountType(source), Source: source, Target: target, ReadOnly: readOnly}, nil
}

func inferSchedulerVolumeMountType(source string) string {
	source = strings.TrimSpace(source)
	if filepath.IsAbs(source) || strings.HasPrefix(source, ".") {
		return domain.VolumeMountTypeBind
	}
	return domain.VolumeMountTypeVolume
}

func schedulerCommandResultValue(jsctx *qjs.Context, apiName string, response domain.SchedulerCommandResult) (*qjs.Value, error) {
	data, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode %s response: %w", apiName, err)
	}
	value, err := payloadValueFromJSON(jsctx, string(data))
	if err != nil {
		return nil, fmt.Errorf("decode %s response: %w", apiName, err)
	}
	return value, nil
}

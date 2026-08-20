package api

import (
	"strings"

	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func ExecEnvMap(items []*agentcomposev2.EnvVarSpec) map[string]string {
	if len(items) == 0 {
		return nil
	}
	result := make(map[string]string, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.GetName())
		if name == "" {
			continue
		}
		result[name] = item.GetValue()
	}
	return result
}

// ExecResultProtoRequest bundles the exec identifiers and originating
// request ExecResultToProto needs alongside the runtime result itself.
type ExecResultProtoRequest struct {
	ExecID    string
	SandboxID string
	RunID     string
	Request   *agentcomposev2.ExecRequest
	Cwd       string
	Result    domain.ExecResult
	ExecErr   error
}

func ExecResultToProto(req ExecResultProtoRequest) *agentcomposev2.ExecResult {
	errorText := ""
	if req.ExecErr != nil {
		errorText = req.ExecErr.Error()
	}
	result := req.Result
	return &agentcomposev2.ExecResult{
		ExecId:    req.ExecID,
		SandboxId: req.SandboxID,
		RunId:     req.RunID,
		Command: &agentcomposev2.ExecCommand{
			Command: req.Request.GetCommand().GetCommand(),
			Args:    append([]string(nil), req.Request.GetCommand().GetArgs()...),
		},
		Cwd:      req.Cwd,
		ExitCode: int32(result.ExitCode),
		Success:  result.Success,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		Output:   result.Output,
		Error:    errorText,
	}
}

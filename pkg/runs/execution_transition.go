package runs

import (
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	domain "agent-compose/pkg/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func transitionFromCommandResult(run domain.ProjectRunRecord, sandbox *domain.Sandbox, commandText string, result domain.ExecResult, execErr error) TransitionRequest {
	artifactsDir := projectRunCommandArtifactsDir(run, sandbox)
	req := TransitionRequest{
		RunID:        run.RunID,
		SandboxID:    sandbox.Summary.ID,
		ExitCode:     result.ExitCode,
		Output:       result.Output,
		ArtifactsDir: artifactsDir,
		LogsPath:     filepath.Join(artifactsDir, "output.txt"),
	}
	resultJSON, err := json.Marshal(map[string]any{
		"mode":     "command",
		"command":  commandText,
		"success":  result.Success,
		"exitCode": result.ExitCode,
	})
	if err == nil {
		req.ResultJSON = string(resultJSON)
	}
	if execErr != nil {
		req.ExitCode = execution.FirstNonZeroInt(req.ExitCode, 1)
		req.Error = fmt.Sprintf("command execution failed: %v", execErr)
		return req
	}
	if !result.Success {
		req.ExitCode = execution.FirstNonZeroInt(req.ExitCode, 1)
		req.Error = "command execution failed"
		if detail := firstNonEmpty(result.Stderr, result.Output, result.Stdout); strings.TrimSpace(detail) != "" {
			req.Error += ": " + strings.TrimSpace(detail)
		}
	}
	return req
}

func transitionFromRuntimeResult(run domain.ProjectRunRecord, sandbox *domain.Sandbox, commandText, logsPath string, accumulated domain.ExecResult, result driverpkg.RuntimeResult, execErr error) TransitionRequest {
	accumulated.ExitCode = result.ExitCode
	accumulated.Success = result.Success
	if strings.TrimSpace(result.Error) != "" {
		accumulated.Success = false
	}
	if execErr == nil && strings.TrimSpace(result.Error) != "" {
		execErr = errors.New(result.Error)
	}
	transition := transitionFromCommandResult(run, sandbox, commandText, accumulated, execErr)
	transition.LogsPath = logsPath
	return transition
}

func transitionFromPromptWrapperResult(run domain.ProjectRunRecord, sandbox *domain.Sandbox, logsPath string, payload []byte, finalText, stopReason, message string) TransitionRequest {
	transition := TransitionRequest{
		RunID:      run.RunID,
		SandboxID:  sandbox.Summary.ID,
		Output:     finalText,
		ResultJSON: string(payload),
		LogsPath:   logsPath,
	}
	if strings.TrimSpace(message) != "" {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("agent execution failed: %s", strings.TrimSpace(message))
		return transition
	}
	if strings.EqualFold(strings.TrimSpace(stopReason), "cancelled") {
		transition.ExitCode = 1
		transition.Error = "agent execution cancelled"
	}
	return transition
}

func promptWrapperTransitionError(transition TransitionRequest) error {
	if strings.EqualFold(strings.TrimSpace(transition.Error), "agent execution cancelled") {
		return context.Canceled
	}
	return errors.New(firstNonEmpty(transition.Error, "agent execution failed"))
}

func transitionFromPromptRuntimeResult(run domain.ProjectRunRecord, sandbox *domain.Sandbox, logsPath string, result driverpkg.RuntimeResult, execErr error) TransitionRequest {
	transition := TransitionRequest{
		RunID:     run.RunID,
		SandboxID: sandbox.Summary.ID,
		LogsPath:  logsPath,
		ExitCode:  result.ExitCode,
		Error:     result.Error,
	}
	if execErr != nil {
		transition.ExitCode = execution.FirstNonZeroInt(transition.ExitCode, 1)
		transition.Error = fmt.Sprintf("agent execution failed: %v", execErr)
	}
	return transition
}

func errorFromRuntimeResult(result driverpkg.RuntimeResult) error {
	if strings.TrimSpace(result.Error) == "" {
		return nil
	}
	return errors.New(result.Error)
}

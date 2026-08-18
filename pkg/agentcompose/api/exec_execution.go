package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"

	appconfig "agent-compose/pkg/config"
	"agent-compose/pkg/execution"
	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func errorFromString(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return errors.New(text)
}

func (h *ExecHandler) executeProjectCommand(ctx context.Context, req *agentcomposev2.ExecRequest, execID string, send execStreamSender) (*agentcomposev2.ExecResult, error) {
	if h.store == nil || h.projects == nil || h.runtime == nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("exec runtime dependencies are required"))
	}
	sandbox, _, err := h.resolveExecTargetSandbox(ctx, req)
	if err != nil {
		return nil, err
	}
	unlock := h.locks.Lock(sandbox.Summary.ID)
	defer unlock()
	sandbox, runID, err := h.resolveExecTargetSandbox(ctx, req)
	if err != nil {
		return nil, err
	}
	command := strings.TrimSpace(req.GetCommand().GetCommand())
	if command == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("exec command is required"))
	}
	if send != nil {
		if err := send(&agentcomposev2.StreamExecResponse{
			EventType: agentcomposev2.StreamExecEventType_STREAM_EXEC_EVENT_TYPE_STARTED,
			ExecId:    execID,
			SandboxId: sandbox.Summary.ID,
			RunId:     runID,
		}); err != nil {
			return nil, connect.NewError(connect.CodeUnknown, err)
		}
	}
	appconfig.ApplyDefaultGuestPaths(h.config)
	cwd := strings.TrimSpace(req.GetCwd())
	if cwd == "" {
		cwd = h.config.GuestWorkspacePath
	}
	vmState, err := h.store.GetVMState(sandbox.Summary.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	runtime, err := h.runtime(sandbox)
	if err != nil {
		return nil, ConnectErrorForDomain(err)
	}
	hostExecDir := filepath.Join(execution.HostSandboxDir(sandbox), "state", "exec", execID)
	if err := os.MkdirAll(hostExecDir, 0o755); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create exec artifact dir: %w", err))
	}
	guestExecDir := filepath.Join(h.config.GuestStateRoot, "exec", execID)
	runtimeRequest := execution.RuntimeCommandRequestPayloadFromCommand(
		h.config,
		"exec",
		command,
		req.GetCommand().GetArgs(),
		"",
		cwd,
		ExecEnvMap(req.GetEnv()),
		int64(req.GetTimeoutMs()),
		int64(req.GetMaxOutputBytes()),
		guestExecDir,
	)
	if err := execution.WriteJSONArtifact(filepath.Join(hostExecDir, "command-request.json"), runtimeRequest); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("write exec command request artifact: %w", err))
	}
	transcriptPath := filepath.Join(hostExecDir, "transcript.txt")
	var sendErr error
	writer := func(chunk domain.ExecChunk) {
		if sendErr != nil {
			return
		}
		filtered, visible := execution.FilterCommandStreamChunk(chunk)
		if !visible {
			return
		}
		if err := appendExecTranscriptChunk(transcriptPath, filtered); err != nil {
			sendErr = err
			return
		}
		if send != nil {
			createdAt := time.Now().UTC()
			sendErr = send(&agentcomposev2.StreamExecResponse{
				EventType:  agentcomposev2.StreamExecEventType_STREAM_EXEC_EVENT_TYPE_OUTPUT,
				ExecId:     execID,
				SandboxId:  sandbox.Summary.ID,
				RunId:      runID,
				Chunk:      filtered.Text,
				Stream:     StdioStreamToProto(filtered.Stream),
				Transcript: TranscriptEventFromExecChunk(filtered, createdAt),
			})
		}
	}
	execCtx, cancel := execution.ExecContext(ctx, req.GetTimeoutMs())
	defer cancel()
	result, execErr := runtime.ExecStream(execCtx, sandbox, vmState, execution.BuildRuntimeCommandExecSpec(h.config, sandbox, filepath.Join(guestExecDir, "command-request.json"), h.config.GuestHomePath), writer)
	if sendErr != nil {
		return nil, connect.NewError(connect.CodeUnknown, sendErr)
	}
	if execErr != nil {
		result.ExitCode = execution.FirstNonZeroInt(result.ExitCode, 1)
		result.Success = false
		if strings.TrimSpace(result.Output) == "" {
			result.Output = firstNonEmpty(result.Stderr, result.Stdout, execErr.Error())
		}
		return ExecResultToProto(execID, sandbox.Summary.ID, runID, req, cwd, result, execErr), nil
	}
	commandResult, err := execution.ParseCommandExecResult(result)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := execution.MirrorRuntimeCommandArtifacts(hostExecDir, commandResult); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return ExecResultToProto(execID, sandbox.Summary.ID, runID, req, cwd, execution.RuntimeCommandResultToExecResult(commandResult), nil), nil
}

func appendExecTranscriptChunk(path string, chunk domain.ExecChunk) error {
	path = strings.TrimSpace(path)
	if path == "" || chunk.Text == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create exec transcript dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open exec transcript %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(chunk.Text); err != nil {
		return fmt.Errorf("append exec transcript %s: %w", path, err)
	}
	return nil
}

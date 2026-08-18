package runs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/projects"
)

func (c *Controller) executeStartedProjectRunAttach(ctx context.Context, run domain.ProjectRunRecord, req RunAgentRequest, warnings []string, start RunAttachInput, mode RunAttachMode, receive RunAttachReceiver, send RunAttachSender) (domain.ProjectRunRecord, error, error) {
	coordinator := NewCoordinator(c.configDB, projects.StableProjectRunID)
	commandText := strings.TrimSpace(req.Command)
	transitionCtx := context.WithoutCancel(ctx)
	prepared, err := c.prepareProjectRun(ctx, run, req.Env)
	if err != nil {
		run, markErr := c.completeProjectRunError(transitionCtx, ctx, TransitionRequest{
			RunID: run.RunID,
			Error: fmt.Sprintf("workspace preparation failed: %v", err),
		}, err)
		return withRunWarnings(run, warnings), err, markErr
	}
	sandboxResult, err := c.ensureProjectRunSandbox(ctx, run, prepared, req)
	if err != nil {
		transition := TransitionRequest{
			RunID: run.RunID,
			Error: fmt.Sprintf("sandbox start failed: %v", err),
		}
		if sandboxResult.Sandbox != nil {
			transition.SandboxID = sandboxResult.Sandbox.Summary.ID
		}
		run, markErr := c.completeProjectRunError(transitionCtx, ctx, transition, err)
		return withRunWarnings(run, warnings), err, markErr
	}
	warnings = append(warnings, sandboxResult.Warnings...)
	run, err = coordinator.MarkRunning(transitionCtx, run.RunID, sandboxResult.Sandbox.Summary.ID)
	if err != nil {
		return domain.ProjectRunRecord{}, nil, err
	}
	run = withRunWarnings(run, warnings)
	var transition TransitionRequest
	var execErr error
	switch mode {
	case RunAttachModePrompt:
		transition, execErr = c.runPromptInteraction(ctx, coordinator, run, sandboxResult.Sandbox, req, start, receive, send)
	default:
		transition, execErr = c.runCommandInteraction(ctx, coordinator, run, sandboxResult.Sandbox, req, commandText, start, receive, send)
	}
	if execErr != nil || transition.ExitCode != 0 {
		run, err = c.completeProjectRunError(transitionCtx, ctx, transition, execErr)
		if err != nil {
			return domain.ProjectRunRecord{}, nil, err
		}
		run = withRunWarnings(run, warnings)
		_ = send(runAttachResultResponse(run, transition, false))
		return run, execErr, nil
	}
	transition.Status = domain.ProjectRunStatusSucceeded
	run, err = c.completeProjectRun(transitionCtx, transition)
	if err != nil {
		return domain.ProjectRunRecord{}, nil, err
	}
	run = withRunWarnings(run, warnings)
	if err := send(runAttachResultResponse(run, transition, true)); err != nil {
		return domain.ProjectRunRecord{}, nil, err
	}
	return run, nil, nil
}

func (c *Controller) runCommandInteraction(ctx context.Context, coordinator *Coordinator, run domain.ProjectRunRecord, sandbox *domain.Sandbox, req RunAgentRequest, commandText string, start RunAttachInput, receive RunAttachReceiver, send RunAttachSender) (TransitionRequest, error) {
	artifactsDir := projectRunCommandArtifactsDir(run, sandbox)
	logsPath := filepath.Join(artifactsDir, "transcript.txt")
	transition := TransitionRequest{RunID: run.RunID, SandboxID: sandbox.Summary.ID, LogsPath: logsPath}
	if c.store == nil || c.runtime == nil {
		err := fmt.Errorf("command runtime dependencies are required")
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("command execution failed: %v", err)
		return transition, err
	}
	appconfig.ApplyDefaultGuestPaths(c.config)
	vmState, err := c.store.GetVMState(sandbox.Summary.ID)
	if err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("command execution failed: %v", err)
		return transition, err
	}
	runtime, err := c.runtime(sandbox)
	if err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("command execution failed: %v", err)
		return transition, err
	}
	interactionRuntime, ok := runtime.(InteractionRuntime)
	if !ok {
		err := fmt.Errorf("%w: command attach is unsupported by this runtime driver", domain.ErrUnsupported)
		transition.ExitCode = 1
		transition.Error = err.Error()
		return transition, err
	}
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("command execution failed: %v", err)
		return transition, err
	}
	run, err = markProjectRunInteractionArtifacts(ctx, coordinator, run, sandbox, logsPath, artifactsDir)
	if err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("command execution failed: %v", err)
		return transition, err
	}
	spec := driverpkg.RuntimeStartSpec{
		OperationID: run.RunID,
		Kind:        driverpkg.RuntimeOperationCommand,
		Origin:      "run_attach",
		Command: &driverpkg.RuntimeCommandSpec{
			Command: "bash",
			Args:    []string{"-lc", commandText},
			Env:     execEnvMap(req.Env),
			Cwd:     c.config.GuestWorkspacePath,
		},
		Cwd:         c.config.GuestWorkspacePath,
		Env:         execEnvMap(req.Env),
		AttachStdin: start.AttachStdin,
		TTY:         start.TTY,
		Rows:        start.Rows,
		Cols:        start.Cols,
	}
	interaction, err := interactionRuntime.OpenInteraction(ctx, sandbox, vmState, spec)
	if err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("command execution failed: %v", err)
		return transition, err
	}
	interaction = driverpkg.GuardRuntimeInteractionInput(interaction)
	defer func() { _ = interaction.CloseSend() }()
	go pumpRunAttachInput(receive, interaction)
	accumulator := execution.ExecStreamAccumulator{}
	for {
		frame, err := interaction.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				result, waitErr := interaction.Wait()
				if waitErr != nil {
					transition.ExitCode = 1
					transition.Error = fmt.Sprintf("command execution failed: %v", waitErr)
					return transition, waitErr
				}
				return transitionFromRuntimeResult(run, sandbox, commandText, logsPath, accumulator.Result(result.ExitCode, result.Success), result, nil), nil
			}
			transition.ExitCode = 1
			transition.Error = fmt.Sprintf("command execution failed: %v", err)
			_ = send(runAttachErrorResponse("runtime_recv_error", err.Error(), true))
			return transition, err
		}
		switch frame.Type {
		case driverpkg.RuntimeOutputStarted:
			if err := send(runAttachStartedResponse(run, sandbox, warningsFromRun(run), frame.StartedAt)); err != nil {
				transition.ExitCode = 1
				transition.Error = fmt.Sprintf("command execution failed: %v", err)
				return transition, err
			}
		case driverpkg.RuntimeOutputStdout, driverpkg.RuntimeOutputStderr:
			stream := driverOutputStreamToRun(frame.Type)
			chunk := domain.ExecChunk{Text: string(frame.Data), Stream: stream}
			accumulator.WriteChunk(chunk)
			offset, err := appendProjectRunLogChunk(logsPath, chunk)
			if err != nil {
				transition.ExitCode = 1
				transition.Error = fmt.Sprintf("command execution failed: %v", err)
				return transition, err
			}
			c.publishRunLogChunk(run.RunID, chunk, offset)
			if err := send(runAttachOutputResponse(frame.Data, stream, start.TTY)); err != nil {
				transition.ExitCode = 1
				transition.Error = fmt.Sprintf("command execution failed: %v", err)
				return transition, err
			}
		case driverpkg.RuntimeOutputResult:
			result := frame.Result
			if result == nil {
				result = &driverpkg.RuntimeResult{OperationID: run.RunID, Success: true}
			}
			return transitionFromRuntimeResult(run, sandbox, commandText, logsPath, accumulator.Result(result.ExitCode, result.Success), *result, errorFromRuntimeResult(*result)), nil
		case driverpkg.RuntimeOutputError:
			code := "runtime_error"
			message := "runtime interaction failed"
			if frame.Error != nil {
				code = firstNonEmpty(frame.Error.Code, code)
				message = firstNonEmpty(frame.Error.Message, message)
			}
			_ = send(runAttachErrorResponse(code, message, true))
			transition.ExitCode = 1
			transition.Error = message
			return transition, errors.New(message)
		}
	}
}

func markProjectRunInteractionArtifacts(ctx context.Context, coordinator *Coordinator, run domain.ProjectRunRecord, sandbox *domain.Sandbox, logsPath, artifactsDir string) (domain.ProjectRunRecord, error) {
	if coordinator == nil || sandbox == nil {
		return run, nil
	}
	return coordinator.TransitionRun(context.WithoutCancel(ctx), TransitionRequest{
		RunID:        run.RunID,
		Status:       domain.ProjectRunStatusRunning,
		SandboxID:    sandbox.Summary.ID,
		LogsPath:     logsPath,
		ArtifactsDir: artifactsDir,
	})
}

func pumpRunAttachInput(receive RunAttachReceiver, interaction driverpkg.RuntimeInteraction) {
	defer func() { _ = interaction.CloseSend() }()
	for {
		req, err := receive()
		if err != nil {
			return
		}
		frame, ok := runtimeInputFrameFromRunAttach(req)
		if !ok {
			_ = interaction.Send(driverpkg.RuntimeInputFrame{Type: driverpkg.RuntimeInputCancel, Message: "invalid run attach frame"})
			return
		}
		if err := interaction.Send(frame); err != nil {
			return
		}
	}
}

func runtimeInputFrameFromRunAttach(req RunAttachInput) (driverpkg.RuntimeInputFrame, bool) {
	switch req.Kind {
	case RunAttachInputStdin:
		return driverpkg.RuntimeInputFrame{Type: driverpkg.RuntimeInputStdin, Data: req.Data}, true
	case RunAttachInputStdinEOF:
		return driverpkg.RuntimeInputFrame{Type: driverpkg.RuntimeInputStdinEOF}, true
	case RunAttachInputResize:
		return driverpkg.RuntimeInputFrame{Type: driverpkg.RuntimeInputResize, Rows: req.Rows, Cols: req.Cols}, true
	case RunAttachInputSignal:
		return driverpkg.RuntimeInputFrame{Type: driverpkg.RuntimeInputSignal, Signal: driverpkg.RuntimeSignal(strings.TrimSpace(req.Signal))}, true
	case RunAttachInputCancel:
		return driverpkg.RuntimeInputFrame{Type: driverpkg.RuntimeInputCancel, Message: req.Reason}, true
	default:
		return driverpkg.RuntimeInputFrame{}, false
	}
}

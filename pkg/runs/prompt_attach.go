package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	"agent-compose/pkg/llms"
	domain "agent-compose/pkg/model"
)

func (c *Controller) runPromptInteraction(ctx context.Context, coordinator *Coordinator, run domain.ProjectRunRecord, sandbox *domain.Sandbox, req RunAgentRequest, _ RunAttachInput, receive RunAttachReceiver, send RunAttachSender) (transitionResult TransitionRequest, returnErr error) {
	artifactsDir := projectRunCommandArtifactsDir(run, sandbox)
	logsPath := filepath.Join(artifactsDir, "transcript.txt")
	transition := TransitionRequest{RunID: run.RunID, SandboxID: sandbox.Summary.ID, LogsPath: logsPath}
	if c.store == nil || c.runtime == nil {
		err := fmt.Errorf("prompt runtime dependencies are required")
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("agent execution failed: %v", err)
		return transition, err
	}
	appconfig.ApplyDefaultGuestPaths(c.config)
	vmState, err := c.store.GetVMState(sandbox.Summary.ID)
	if err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("agent execution failed: %v", err)
		return transition, err
	}
	runtime, err := c.runtime(sandbox)
	if err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("agent execution failed: %v", err)
		return transition, err
	}
	interactionRuntime, ok := runtime.(InteractionRuntime)
	if !ok {
		err := fmt.Errorf("%w: prompt attach is unsupported by this runtime driver", domain.ErrUnsupported)
		transition.ExitCode = 1
		transition.Error = err.Error()
		return transition, err
	}
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("agent execution failed: %v", err)
		return transition, err
	}
	run, err = markProjectRunInteractionArtifacts(ctx, coordinator, run, sandbox, logsPath, artifactsDir)
	if err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("agent execution failed: %v", err)
		return transition, err
	}
	agentConfig, err := c.projectRunAgentConfig(ctx, run)
	if err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("agent execution failed: %v", err)
		return transition, err
	}
	if agentConfig.Provider != "codex" && agentConfig.Provider != "claude" && agentConfig.Provider != "opencode" && agentConfig.Provider != "pi" {
		err := fmt.Errorf("%w: prompt attach currently supports codex, claude, opencode, and pi providers only", domain.ErrUnsupported)
		transition.ExitCode = 1
		transition.Error = err.Error()
		return transition, err
	}
	systemPrompt, err := c.projectRunAgentSystemPrompt(ctx, run)
	if err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("agent execution failed: %v", err)
		return transition, err
	}
	if err := execution.WriteAgentSystemPromptFile(sandbox, systemPrompt); err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("agent execution failed: %v", err)
		return transition, err
	}
	schemaPath, err := execution.WriteAgentOutputSchemaFile(c.config, sandbox, agentConfig.Provider, req.OutputSchemaJSON)
	if err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("agent execution failed: %v", err)
		return transition, err
	}
	env := execution.BuildSandboxExecEnv(c.config, sandbox, c.config.GuestHomePath)
	managedEnv, err := c.ensurePromptAttachLLMFacadeEnv(ctx, sandbox, agentConfig, run.RunID)
	if err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("agent execution failed: %v", err)
		return transition, err
	}
	if len(managedEnv) > 0 {
		env = llms.MergeManagedExecEnv(env, managedEnv)
		if token := managedEnv["AGENT_COMPOSE_SANDBOX_TOKEN"]; token != "" {
			defer func() {
				if !errors.Is(returnErr, domain.ErrExecTerminationUnconfirmed) && !errors.Is(returnErr, driverpkg.ErrExecTerminationUnconfirmed) {
					c.deletePromptAttachLLMFacadeToken(context.WithoutCancel(ctx), token)
				}
			}()
		}
	}
	command := strings.Join([]string{
		"set -e",
		"cd " + execution.ShellQuote(c.config.GuestWorkspacePath),
		"mkdir -p " + execution.ShellQuote(c.config.GuestHomePath),
		"agent-compose-runtime stream",
	}, " && ")
	spec := driverpkg.RuntimeStartSpec{
		OperationID: run.RunID,
		Kind:        driverpkg.RuntimeOperationCommand,
		Origin:      "run_prompt_attach",
		Command: &driverpkg.RuntimeCommandSpec{
			Command: "sh",
			Args:    []string{"-lc", command},
			Env:     env,
			Cwd:     c.config.GuestWorkspacePath,
		},
		Cwd:         c.config.GuestWorkspacePath,
		Env:         env,
		AttachStdin: true,
		TTY:         false,
	}
	interaction, err := interactionRuntime.OpenInteraction(ctx, sandbox, vmState, spec)
	if err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("agent execution failed: %v", err)
		return transition, err
	}
	interaction = driverpkg.GuardRuntimeInteractionInput(interaction)
	defer func() { _ = interaction.CloseSend() }()
	projector := newPersistentPromptAttachProjector(context.WithoutCancel(ctx), persistentPromptAttachProjectorDeps{Run: run, Sandbox: sandbox, LogsPath: logsPath, Hub: c.runLogs, EventStore: c.configDB})
	input := &promptWrapperInput{interaction: interaction}
	if err := input.Start(agentConfig, c.config, schemaPath); err != nil {
		transition.ExitCode = 1
		transition.Error = fmt.Sprintf("agent execution failed: %v", err)
		return transition, err
	}
	inputCtx, cancelInput := context.WithCancel(ctx)
	defer cancelInput()
	turnReady := make(chan struct{}, 1)
	if prompt := strings.TrimSpace(req.Prompt); prompt != "" {
		if err := input.HumanMessage(prompt); err != nil {
			transition.ExitCode = 1
			transition.Error = fmt.Sprintf("agent execution failed: %v", err)
			return transition, err
		}
	} else {
		releasePromptTurn(turnReady)
	}
	go pumpRunPromptAttachInput(inputCtx, receive, promptInputPump{Input: input, TurnReady: turnReady, OnHumanMessage: projector.AppendHumanMessageFrame})
	var promptTransition *TransitionRequest
	for {
		frame, err := interaction.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				result, waitErr := interaction.Wait()
				if waitErr != nil {
					transition.ExitCode = 1
					transition.Error = fmt.Sprintf("agent execution failed: %v", waitErr)
					return transition, waitErr
				}
				if promptTransition != nil {
					if result.ExitCode != 0 || !result.Success {
						promptTransition.ExitCode = execution.FirstNonZeroInt(result.ExitCode, promptTransition.ExitCode)
						promptTransition.Error = firstNonEmpty(promptTransition.Error, result.Error, "agent execution failed")
						return *promptTransition, promptWrapperTransitionError(*promptTransition)
					}
					if promptTransition.ExitCode != 0 || strings.TrimSpace(promptTransition.Error) != "" {
						return *promptTransition, promptWrapperTransitionError(*promptTransition)
					}
					return *promptTransition, nil
				}
				return transitionFromPromptRuntimeResult(run, sandbox, promptRuntimeOutcome{LogsPath: logsPath, Result: result, Err: errorFromRuntimeResult(result)}), errorFromRuntimeResult(result)
			}
			transition.ExitCode = 1
			transition.Error = fmt.Sprintf("agent execution failed: %v", err)
			_ = send(runAttachErrorResponse("runtime_recv_error", err.Error(), true))
			return transition, err
		}
		switch frame.Type {
		case driverpkg.RuntimeOutputStarted:
			if err := send(runAttachStartedResponse(run, sandbox, warningsFromRun(run), frame.StartedAt)); err != nil {
				transition.ExitCode = 1
				transition.Error = fmt.Sprintf("agent execution failed: %v", err)
				return transition, err
			}
		case driverpkg.RuntimeOutputStdout:
			responses, nextTransition, err := projector.Project(frame.Data)
			if err != nil {
				_ = send(runAttachErrorResponse("runtime_stream_decode_error", err.Error(), true))
				transition.ExitCode = 1
				transition.Error = fmt.Sprintf("agent execution failed: %v", err)
				return transition, err
			}
			for _, resp := range responses {
				if err := send(resp); err != nil {
					transition.ExitCode = 1
					transition.Error = fmt.Sprintf("agent execution failed: %v", err)
					return transition, err
				}
				if resp.Kind == RunAttachOutputAgentTurnCompleted {
					releasePromptTurn(turnReady)
				}
			}
			if nextTransition != nil {
				promptTransition = nextTransition
			}
		case driverpkg.RuntimeOutputStderr:
			if err := projector.AppendStderr(string(frame.Data)); err != nil {
				transition.ExitCode = 1
				transition.Error = fmt.Sprintf("agent execution failed: %v", err)
				return transition, err
			}
			if err := send(runAttachOutputResponse(frame.Data, domain.StdioStderr, false)); err != nil {
				transition.ExitCode = 1
				transition.Error = fmt.Sprintf("agent execution failed: %v", err)
				return transition, err
			}
		case driverpkg.RuntimeOutputResult:
			result := frame.Result
			if result == nil {
				result = &driverpkg.RuntimeResult{OperationID: run.RunID, Success: true}
			}
			if promptTransition != nil {
				if result.ExitCode != 0 || !result.Success {
					promptTransition.ExitCode = execution.FirstNonZeroInt(result.ExitCode, promptTransition.ExitCode)
					promptTransition.Error = firstNonEmpty(promptTransition.Error, result.Error, "agent execution failed")
					return *promptTransition, promptWrapperTransitionError(*promptTransition)
				}
				if promptTransition.ExitCode != 0 || strings.TrimSpace(promptTransition.Error) != "" {
					return *promptTransition, promptWrapperTransitionError(*promptTransition)
				}
				return *promptTransition, nil
			}
			return transitionFromPromptRuntimeResult(run, sandbox, promptRuntimeOutcome{LogsPath: logsPath, Result: *result, Err: errorFromRuntimeResult(*result)}), errorFromRuntimeResult(*result)
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

type promptWrapperInput struct {
	interaction driverpkg.RuntimeInteraction
	seq         int
}

func (w *promptWrapperInput) Start(agent execution.AgentConfig, config *appconfig.Config, schemaPath string) error {
	frame := map[string]any{
		"v":         1,
		"seq":       w.nextSeq(),
		"type":      "start",
		"provider":  agent.Provider,
		"stateRoot": config.GuestStateRoot,
		"workspace": config.GuestWorkspacePath,
		"home":      config.GuestHomePath,
	}
	if strings.TrimSpace(agent.Model) != "" {
		frame["model"] = strings.TrimSpace(agent.Model)
	}
	if strings.TrimSpace(schemaPath) != "" {
		frame["outputSchemaFile"] = strings.TrimSpace(schemaPath)
	}
	return w.send(frame)
}

func (w *promptWrapperInput) HumanMessage(message string) error {
	return w.send(map[string]any{
		"v":       1,
		"seq":     w.nextSeq(),
		"type":    "human_message",
		"message": message,
	})
}

func (w *promptWrapperInput) EOF() error {
	return w.send(map[string]any{"v": 1, "seq": w.nextSeq(), "type": "eof"})
}

func (w *promptWrapperInput) Cancel(reason string) error {
	return w.send(map[string]any{"v": 1, "seq": w.nextSeq(), "type": "cancel", "message": reason})
}

func (w *promptWrapperInput) nextSeq() int {
	seq := w.seq
	w.seq++
	return seq
}

func (w *promptWrapperInput) send(frame map[string]any) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return w.interaction.Send(driverpkg.RuntimeInputFrame{Type: driverpkg.RuntimeInputStdin, Data: data})
}

// promptInputPump groups the wrapper input stream, the gate that releases a
// turn, and the callback notified of each forwarded human message.
type promptInputPump struct {
	Input          *promptWrapperInput
	TurnReady      <-chan struct{}
	OnHumanMessage func(string, string) error
}

func pumpRunPromptAttachInput(ctx context.Context, receive RunAttachReceiver, pump promptInputPump) {
	defer func() { _ = pump.Input.interaction.CloseSend() }()
	for {
		req, err := receive()
		if err != nil {
			_ = pump.Input.EOF()
			return
		}
		switch req.Kind {
		case RunAttachInputHumanMessage:
			if !forwardPromptHumanMessage(ctx, pump, req.Text, req.ClientFrameID) {
				return
			}
		case RunAttachInputStdin:
			if !forwardPromptHumanMessage(ctx, pump, string(req.Data), req.ClientFrameID) {
				return
			}
		case RunAttachInputStdinEOF:
			_ = pump.Input.EOF()
			return
		case RunAttachInputCancel:
			_ = pump.Input.Cancel(req.Reason)
			return
		default:
			_ = pump.Input.Cancel("invalid run prompt attach frame")
			return
		}
	}
}

func forwardPromptHumanMessage(ctx context.Context, pump promptInputPump, text, clientFrameID string) bool {
	if pump.TurnReady != nil {
		select {
		case <-ctx.Done():
			return false
		case <-pump.TurnReady:
		}
	}
	if pump.OnHumanMessage != nil {
		if err := pump.OnHumanMessage(text, clientFrameID); err != nil {
			return false
		}
	}
	return pump.Input.HumanMessage(text) == nil
}

func releasePromptTurn(turnReady chan<- struct{}) {
	select {
	case turnReady <- struct{}{}:
	default:
	}
}

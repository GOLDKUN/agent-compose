package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func (h *ExecHandler) AttachExec(ctx context.Context, stream *connect.BidiStream[agentcomposev2.AttachExecRequest, agentcomposev2.AttachExecResponse]) error {
	return h.execAttach(ctx, stream.Receive, stream.Send)
}

type execStreamSender func(*agentcomposev2.StreamExecResponse) error
type execAttachReceiver func() (*agentcomposev2.AttachExecRequest, error)
type execAttachSender func(*agentcomposev2.AttachExecResponse) error

func (h *ExecHandler) execAttach(ctx context.Context, receive execAttachReceiver, send execAttachSender) error {
	if h.store == nil || h.projects == nil || h.runtime == nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("exec runtime dependencies are required"))
	}
	if receive == nil || send == nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("exec attach stream is required"))
	}
	first, err := receive()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("exec attach start frame is required"))
		}
		return connect.NewError(connect.CodeUnknown, err)
	}
	start := first.GetStart()
	if start == nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("first exec attach frame must be start"))
	}
	mode := start.GetMode()
	if mode == agentcomposev2.AttachRunMode_ATTACH_RUN_MODE_PROMPT || strings.TrimSpace(start.GetPrompt()) != "" {
		return h.execPromptAttach(ctx, start, receive, send)
	}
	state, err := h.prepareExecAttach(ctx, start)
	if err != nil {
		return err
	}
	unlock := h.locks.Lock(state.sandbox.Summary.ID)
	defer unlock()
	state, err = h.prepareExecAttach(ctx, start)
	if err != nil {
		return err
	}
	runner := execAttachRunner{
		state:   state,
		receive: receive,
		send:    send,
	}
	return runner.run(ctx)
}

type execAttachRunner struct {
	state   *execAttachState
	receive execAttachReceiver
	send    execAttachSender
}

func (r execAttachRunner) run(ctx context.Context) error {
	interactionRuntime, ok := r.state.runtime.(ExecInteractionRuntime)
	if !ok {
		return connect.NewError(connect.CodeUnimplemented, fmt.Errorf("exec attach is unsupported by this runtime driver"))
	}
	interaction, err := interactionRuntime.OpenInteraction(ctx, r.state.sandbox, r.state.vmState, r.state.spec)
	if err != nil {
		if errors.Is(err, driverpkg.ErrRuntimeInteractionUnsupported) {
			return connect.NewError(connect.CodeUnimplemented, err)
		}
		return connect.NewError(connect.CodeInternal, err)
	}
	interaction = driverpkg.GuardRuntimeInteractionInput(interaction)
	defer func() { _ = interaction.CloseSend() }()
	return r.runInteraction(interaction)
}

func (r execAttachRunner) runInteraction(interaction driverpkg.RuntimeInteraction) error {
	go pumpExecAttachInput(r.receive, interaction)

	projection := newExecAttachProjection(r.state)
	for {
		frame, err := interaction.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return sendExecAttachError(r.send, "runtime_recv_error", err.Error(), true)
		}
		resp := projection.responseFromRuntimeFrame(frame)
		if resp == nil {
			continue
		}
		if err := r.send(resp); err != nil {
			return connect.NewError(connect.CodeUnknown, err)
		}
	}
}

func pumpExecAttachInput(receive execAttachReceiver, interaction driverpkg.RuntimeInteraction) {
	defer func() { _ = interaction.CloseSend() }()
	for {
		req, err := receive()
		if err != nil {
			return
		}
		frame, ok := runtimeInputFrameFromExecAttach(req)
		if !ok {
			_ = interaction.Send(driverpkg.RuntimeInputFrame{Type: driverpkg.RuntimeInputCancel, Message: "invalid exec attach frame"})
			return
		}
		if err := interaction.Send(frame); err != nil {
			return
		}
	}
}

func runtimeInputFrameFromExecAttach(req *agentcomposev2.AttachExecRequest) (driverpkg.RuntimeInputFrame, bool) {
	switch frame := req.GetFrame().(type) {
	case *agentcomposev2.AttachExecRequest_Stdin:
		return driverpkg.RuntimeInputFrame{Type: driverpkg.RuntimeInputStdin, Data: frame.Stdin.GetData()}, true
	case *agentcomposev2.AttachExecRequest_StdinEof:
		return driverpkg.RuntimeInputFrame{Type: driverpkg.RuntimeInputStdinEOF}, true
	case *agentcomposev2.AttachExecRequest_Resize:
		size := frame.Resize.GetTerminalSize()
		return driverpkg.RuntimeInputFrame{Type: driverpkg.RuntimeInputResize, Rows: size.GetRows(), Cols: size.GetCols()}, true
	case *agentcomposev2.AttachExecRequest_Signal:
		return driverpkg.RuntimeInputFrame{Type: driverpkg.RuntimeInputSignal, Signal: driverpkg.RuntimeSignal(strings.TrimSpace(frame.Signal.GetSignal()))}, true
	case *agentcomposev2.AttachExecRequest_Cancel:
		return driverpkg.RuntimeInputFrame{Type: driverpkg.RuntimeInputCancel, Message: frame.Cancel.GetReason()}, true
	default:
		return driverpkg.RuntimeInputFrame{}, false
	}
}

type execAttachProjection struct {
	state       *execAttachState
	accumulator execution.ExecStreamAccumulator
}

func newExecAttachProjection(state *execAttachState) *execAttachProjection {
	return &execAttachProjection{state: state}
}

func (p *execAttachProjection) responseFromRuntimeFrame(frame driverpkg.RuntimeOutputFrame) *agentcomposev2.AttachExecResponse {
	return p.state.responseFromRuntimeFrame(frame, &p.accumulator)
}

func (s *execAttachState) responseFromRuntimeFrame(frame driverpkg.RuntimeOutputFrame, accumulator *execution.ExecStreamAccumulator) *agentcomposev2.AttachExecResponse {
	resp := newAttachExecResponse()
	switch frame.Type {
	case driverpkg.RuntimeOutputStarted:
		resp.Frame = &agentcomposev2.AttachExecResponse_Started{Started: &agentcomposev2.AttachStarted{
			OperationId: s.execID,
			ExecId:      s.execID,
			RunId:       s.runID,
			SandboxId:   s.sandbox.Summary.ID,
		}}
	case driverpkg.RuntimeOutputStdout, driverpkg.RuntimeOutputStderr:
		stream := driverOutputStreamToProto(frame.Type)
		if accumulator != nil {
			accumulator.WriteChunk(domain.ExecChunk{Text: string(frame.Data), Stream: protoStreamToDomain(stream)})
		}
		resp.Frame = &agentcomposev2.AttachExecResponse_Output{Output: &agentcomposev2.AttachOutput{
			Data:   append([]byte(nil), frame.Data...),
			Stream: stream,
			Tty:    s.tty,
			Transcript: &agentcomposev2.TranscriptEvent{
				Stream:    stream,
				Text:      string(frame.Data),
				CreatedAt: resp.CreatedAt,
			},
		}}
	case driverpkg.RuntimeOutputResult:
		result := frame.Result
		if result == nil {
			result = &driverpkg.RuntimeResult{OperationID: s.execID}
		}
		accumulated := domain.ExecResult{}
		if accumulator != nil {
			accumulated = accumulator.Result(result.ExitCode, result.Success)
		}
		if strings.TrimSpace(result.Error) != "" {
			accumulated.Success = false
		}
		execResult := ExecResultToProto(s.execID, s.sandbox.Summary.ID, s.runID, s.request, s.cwd, accumulated, errorFromString(result.Error))
		resp.Frame = &agentcomposev2.AttachExecResponse_Result{Result: &agentcomposev2.AttachResult{
			ExitCode:   int32(result.ExitCode),
			Success:    result.Success,
			ExecResult: execResult,
			Output:     accumulated.Output,
			Error:      result.Error,
		}}
	case driverpkg.RuntimeOutputError:
		code := "runtime_error"
		message := "runtime interaction failed"
		if frame.Error != nil {
			code = firstNonEmpty(frame.Error.Code, code)
			message = firstNonEmpty(frame.Error.Message, message)
		}
		resp.Frame = &agentcomposev2.AttachExecResponse_Error{Error: &agentcomposev2.AttachError{Code: code, Message: message, Terminal: true}}
	default:
		return nil
	}
	return resp
}

func newAttachExecResponse() *agentcomposev2.AttachExecResponse {
	return &agentcomposev2.AttachExecResponse{
		ServerFrameId: uuid.NewString(),
		CreatedAt:     timestamppb.Now(),
	}
}

func driverOutputStreamToProto(frameType driverpkg.RuntimeOutputFrameType) agentcomposev2.StdioStream {
	if frameType == driverpkg.RuntimeOutputStderr {
		return agentcomposev2.StdioStream_STDIO_STREAM_STDERR
	}
	return agentcomposev2.StdioStream_STDIO_STREAM_STDOUT
}

func protoStreamToDomain(stream agentcomposev2.StdioStream) domain.StdioStream {
	if stream == agentcomposev2.StdioStream_STDIO_STREAM_STDERR {
		return domain.StdioStderr
	}
	return domain.StdioStdout
}

func sendExecAttachError(send execAttachSender, code, message string, terminal bool) error {
	resp := newAttachExecResponse()
	resp.Frame = &agentcomposev2.AttachExecResponse_Error{Error: &agentcomposev2.AttachError{
		Code:     code,
		Message:  message,
		Terminal: terminal,
	}}
	if err := send(resp); err != nil {
		return connect.NewError(connect.CodeUnknown, err)
	}
	return nil
}

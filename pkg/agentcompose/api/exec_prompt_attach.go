package api

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	domain "github.com/chaitin/agent-compose/pkg/model"
	"github.com/chaitin/agent-compose/pkg/runs"
	agentcomposev2 "github.com/chaitin/agent-compose/proto/agentcompose/v2"
)

func (h *ExecHandler) execPromptAttach(ctx context.Context, start *agentcomposev2.AttachExecStart, receive execAttachReceiver, send execAttachSender) error {
	if h.runAttach == nil {
		return connect.NewError(connect.CodeUnimplemented, fmt.Errorf("exec prompt attach is unsupported"))
	}
	req := start.GetRequest()
	if req == nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("exec attach request is required"))
	}
	sandbox, runID, err := h.resolveExecTargetSandbox(ctx, req)
	if err != nil {
		return err
	}
	var run domain.ProjectRunRecord
	if strings.TrimSpace(runID) != "" {
		run, err = h.projects.GetProjectRun(ctx, runID)
		if err != nil {
			return connect.NewError(connect.CodeInternal, err)
		}
	} else {
		run, err = h.latestSandboxRun(ctx, sandbox.Summary.ID)
		if err != nil {
			return err
		}
	}
	projectID := strings.TrimSpace(run.ProjectID)
	agentName := strings.TrimSpace(run.AgentName)
	if projectID == "" || agentName == "" {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("exec prompt target must be associated with a project agent"))
	}
	initial := &agentcomposev2.AttachAgentRunRequest{
		Frame: &agentcomposev2.AttachAgentRunRequest_Start{Start: &agentcomposev2.AttachAgentRunStart{
			Request: &agentcomposev2.RunAgentRequest{
				ProjectId:     projectID,
				AgentName:     agentName,
				Prompt:        strings.TrimSpace(start.GetPrompt()),
				SandboxId:     sandbox.Summary.ID,
				Source:        agentcomposev2.RunSource_RUN_SOURCE_MANUAL,
				CleanupPolicy: agentcomposev2.RunSandboxCleanupPolicy_RUN_SANDBOX_CLEANUP_POLICY_KEEP_RUNNING,
			},
			Mode:        agentcomposev2.AttachRunMode_ATTACH_RUN_MODE_PROMPT,
			AttachStdin: start.GetAttachStdin(),
			Tty:         false,
		}},
	}
	receiver := newExecPromptRunAttachReceiver(initial, receive)
	return h.runAttach.RunProjectCommandAttach(ctx, receiver.Receive, func(output runs.RunAttachOutput) error {
		return send(execAttachResponseFromRunAttach(RunAttachOutputToProto(output)))
	})
}

type execPromptRunAttachReceiver struct {
	initial   *agentcomposev2.AttachAgentRunRequest
	receive   execAttachReceiver
	sentStart bool
}

func newExecPromptRunAttachReceiver(initial *agentcomposev2.AttachAgentRunRequest, receive execAttachReceiver) *execPromptRunAttachReceiver {
	return &execPromptRunAttachReceiver{initial: initial, receive: receive}
}

func (r *execPromptRunAttachReceiver) Receive() (*agentcomposev2.AttachAgentRunRequest, error) {
	if !r.sentStart {
		r.sentStart = true
		return r.initial, nil
	}
	req, err := r.receive()
	if err != nil {
		return nil, err
	}
	switch frame := req.GetFrame().(type) {
	case *agentcomposev2.AttachExecRequest_StdinEof:
		return &agentcomposev2.AttachAgentRunRequest{ClientFrameId: req.GetClientFrameId(), Frame: &agentcomposev2.AttachAgentRunRequest_StdinEof{StdinEof: frame.StdinEof}}, nil
	case *agentcomposev2.AttachExecRequest_Resize:
		return &agentcomposev2.AttachAgentRunRequest{ClientFrameId: req.GetClientFrameId(), Frame: &agentcomposev2.AttachAgentRunRequest_Resize{Resize: frame.Resize}}, nil
	case *agentcomposev2.AttachExecRequest_Cancel:
		return &agentcomposev2.AttachAgentRunRequest{ClientFrameId: req.GetClientFrameId(), Frame: &agentcomposev2.AttachAgentRunRequest_Cancel{Cancel: frame.Cancel}}, nil
	case *agentcomposev2.AttachExecRequest_HumanMessage:
		return &agentcomposev2.AttachAgentRunRequest{ClientFrameId: req.GetClientFrameId(), Frame: &agentcomposev2.AttachAgentRunRequest_HumanMessage{HumanMessage: frame.HumanMessage}}, nil
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid exec prompt attach frame"))
	}
}

func execAttachResponseFromRunAttach(resp *agentcomposev2.AttachAgentRunResponse) *agentcomposev2.AttachExecResponse {
	out := &agentcomposev2.AttachExecResponse{
		ServerFrameId: resp.GetServerFrameId(),
		CreatedAt:     resp.GetCreatedAt(),
	}
	switch frame := resp.GetFrame().(type) {
	case *agentcomposev2.AttachAgentRunResponse_Started:
		out.Frame = &agentcomposev2.AttachExecResponse_Started{Started: frame.Started}
	case *agentcomposev2.AttachAgentRunResponse_Output:
		out.Frame = &agentcomposev2.AttachExecResponse_Output{Output: frame.Output}
	case *agentcomposev2.AttachAgentRunResponse_Result:
		out.Frame = &agentcomposev2.AttachExecResponse_Result{Result: frame.Result}
	case *agentcomposev2.AttachAgentRunResponse_Error:
		out.Frame = &agentcomposev2.AttachExecResponse_Error{Error: frame.Error}
	case *agentcomposev2.AttachAgentRunResponse_AgentEvent:
		out.Frame = &agentcomposev2.AttachExecResponse_AgentEvent{AgentEvent: frame.AgentEvent}
	case *agentcomposev2.AttachAgentRunResponse_AgentTurnCompleted:
		out.Frame = &agentcomposev2.AttachExecResponse_AgentTurnCompleted{AgentTurnCompleted: frame.AgentTurnCompleted}
	}
	return out
}

func (h *ExecHandler) latestSandboxRun(ctx context.Context, sandboxID string) (domain.ProjectRunRecord, error) {
	runsForSandbox, err := h.projects.ListProjectSandboxRuns(ctx, domain.ProjectSandboxRelationFilter{SandboxID: sandboxID, Limit: 1})
	if err != nil {
		return domain.ProjectRunRecord{}, connect.NewError(connect.CodeInternal, err)
	}
	if len(runsForSandbox) == 0 {
		return domain.ProjectRunRecord{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("exec prompt target must be associated with a project run"))
	}
	return runsForSandbox[0], nil
}

package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/samber/do/v2"

	"agent-compose/pkg/agentcompose/adapters"
	"agent-compose/pkg/agentcompose/api"
	"agent-compose/pkg/capabilities"
	appconfig "agent-compose/pkg/config"
	"agent-compose/pkg/dashboard"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/runs"
	"agent-compose/pkg/sandboxes"
	"agent-compose/pkg/schedulers"
	"agent-compose/pkg/storage/configstore"
	"agent-compose/pkg/storage/sandboxstore"
	"agent-compose/pkg/volumes"
	"agent-compose/pkg/workspaces"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func NewRunController(di do.Injector) (*runs.Controller, error) {
	var dashboardHub runs.DashboardNotifier
	if hub, err := do.Invoke[*dashboard.Hub](di); err == nil {
		dashboardHub = hub
	}
	imageBackends := do.MustInvoke[*adapters.ImageBackends](di)
	runtimeProvider := do.MustInvoke[adapters.RuntimeProvider](di)
	return runs.NewController(runs.ControllerDependencies{
		Config:           do.MustInvoke[*appconfig.Config](di),
		Store:            do.MustInvoke[*sandboxstore.Store](di),
		ConfigDB:         do.MustInvoke[*configstore.ConfigStore](di),
		WorkspaceEnsurer: do.MustInvoke[workspaces.WorkspaceEnsurer](di),
		Driver:           do.MustInvoke[*adapters.SandboxDriver](di),
		Executor:         do.MustInvoke[*adapters.AgentExecutor](di),
		Runtime: func(session *domain.Sandbox) (runs.Runtime, error) {
			return runtimeProvider.ForSession(session)
		},
		Images:          imageBackends.Auto,
		SchedulerEngine: do.MustInvoke[schedulers.SchedulerEngine](di),
		Cap:             do.MustInvoke[capabilities.Provider](di),
		Volumes:         do.MustInvoke[*volumes.Manager](di),
		Streams:         do.MustInvoke[*sandboxes.StreamBroker](di),
		Bus:             do.MustInvoke[*schedulers.Bus](di),
		Dashboard:       dashboardHub,
		CapTokens:       do.MustInvoke[*adapters.CapabilitySandboxResolver](di),
		RunLogs:         do.MustInvoke[*runs.RunLogHub](di),
		LifecycleLocks:  do.MustInvoke[*sandboxes.LifecycleLocks](di),
		Removal:         do.MustInvoke[*sandboxes.RemovalCoordinator](di),
	}), nil
}

type runControllerDelegate struct {
	controller *runs.Controller
	supervisor *RunSupervisor
}

func (d runControllerDelegate) RunProjectCommandAttach(ctx context.Context, receive runs.RunAttachReceiver, send runs.RunAttachSender) error {
	return runConnectError(d.controller.RunProjectCommandAttach(ctx, receive, send))
}

func (d runControllerDelegate) RunAgent(ctx context.Context, req *connect.Request[agentcomposev2.RunAgentRequest]) (*connect.Response[agentcomposev2.RunAgentResponse], error) {
	run, _, err := d.controller.RunProjectAgent(ctx, runAgentRequestFromProto(req.Msg), nil)
	if err != nil {
		return nil, runConnectError(err)
	}
	return connect.NewResponse(&agentcomposev2.RunAgentResponse{
		Run:      api.ProjectRunDetailToProto(run),
		Warnings: append([]string(nil), run.Warnings...),
	}), nil
}

func (d runControllerDelegate) StartAgentRun(ctx context.Context, req *connect.Request[agentcomposev2.StartAgentRunRequest]) (*connect.Response[agentcomposev2.StartAgentRunResponse], error) {
	if d.supervisor == nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("run supervisor is required"))
	}
	run, err := d.supervisor.StartRun(ctx, runAgentRequestFromProto(req.Msg.GetRun()))
	if err != nil {
		return nil, runConnectError(err)
	}
	return connect.NewResponse(&agentcomposev2.StartAgentRunResponse{
		Run:      api.ProjectRunSummaryToProto(run),
		Warnings: append([]string(nil), run.Warnings...),
		Started:  !runs.StatusIsTerminal(run.Status),
	}), nil
}

func (d runControllerDelegate) StreamAgentRun(ctx context.Context, req *connect.Request[agentcomposev2.RunAgentRequest], stream *connect.ServerStream[agentcomposev2.StreamAgentRunResponse]) error {
	runs.PrepareStreamingHeaders(stream.ResponseHeader())
	sink := runs.StreamSink{
		SendStarted: func(run domain.ProjectRunRecord, createdAt time.Time) error {
			return sendRunAgentStreamStarted(stream, run, createdAt)
		},
		SendChunk: func(runID string, chunk domain.ExecChunk, createdAt time.Time) error {
			return sendRunAgentStreamChunk(stream, runID, chunk, createdAt)
		},
	}
	run, execErr, err := d.controller.RunProjectAgent(ctx, runAgentRequestFromProto(req.Msg), &sink)
	if err != nil {
		return runConnectError(err)
	}
	if errors.Is(execErr, runs.ErrRunAgentStreamSend) {
		return connect.NewError(connect.CodeUnknown, execErr)
	}
	if sendErr := stream.Send(runAgentStreamCompletedProjection(run, time.Now().UTC())); sendErr != nil {
		return connect.NewError(connect.CodeUnknown, sendErr)
	}
	return nil
}

func sendRunAgentStreamStarted(stream *connect.ServerStream[agentcomposev2.StreamAgentRunResponse], run domain.ProjectRunRecord, createdAt time.Time) error {
	if err := stream.Send(runAgentStreamStartedProjection(run, createdAt)); err != nil {
		return fmt.Errorf("%w: %w", runs.ErrRunAgentStreamSend, err)
	}
	return nil
}

func sendRunAgentStreamChunk(stream *connect.ServerStream[agentcomposev2.StreamAgentRunResponse], runID string, chunk domain.ExecChunk, createdAt time.Time) error {
	if err := stream.Send(runAgentStreamChunkProjection(runID, chunk, createdAt)); err != nil {
		return fmt.Errorf("%w: %w", runs.ErrRunAgentStreamSend, err)
	}
	return nil
}

func runAgentStreamStartedProjection(run domain.ProjectRunRecord, createdAt time.Time) *agentcomposev2.StreamAgentRunResponse {
	return &agentcomposev2.StreamAgentRunResponse{
		EventType: agentcomposev2.StreamAgentRunEventType_STREAM_AGENT_RUN_EVENT_TYPE_STARTED,
		Run:       api.ProjectRunSummaryToProto(run),
		RunId:     run.RunID,
		CreatedAt: api.FormatProjectTime(createdAt),
		Warnings:  append([]string(nil), run.Warnings...),
	}
}

func runAgentStreamChunkProjection(runID string, chunk domain.ExecChunk, createdAt time.Time) *agentcomposev2.StreamAgentRunResponse {
	return &agentcomposev2.StreamAgentRunResponse{
		EventType:  agentcomposev2.StreamAgentRunEventType_STREAM_AGENT_RUN_EVENT_TYPE_OUTPUT,
		RunId:      runID,
		Chunk:      chunk.Text,
		Stream:     api.StdioStreamToProto(chunk.Stream),
		CreatedAt:  api.FormatProjectTime(createdAt),
		Transcript: api.TranscriptEventFromExecChunk(chunk, createdAt),
	}
}

func runAgentStreamCompletedProjection(run domain.ProjectRunRecord, createdAt time.Time) *agentcomposev2.StreamAgentRunResponse {
	return &agentcomposev2.StreamAgentRunResponse{
		EventType: agentcomposev2.StreamAgentRunEventType_STREAM_AGENT_RUN_EVENT_TYPE_COMPLETED,
		Run:       api.ProjectRunSummaryToProto(run),
		RunId:     run.RunID,
		CreatedAt: api.FormatProjectTime(createdAt),
		Warnings:  append([]string(nil), run.Warnings...),
	}
}

func (d runControllerDelegate) AttachAgentRun(ctx context.Context, stream *connect.BidiStream[agentcomposev2.AttachAgentRunRequest, agentcomposev2.AttachAgentRunResponse]) error {
	if d.controller == nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("run controller is required"))
	}
	if err := d.controller.RunProjectCommandAttach(ctx, stream.Receive, stream.Send); err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return connectErr
		}
		return runConnectError(err)
	}
	return nil
}

func runAgentRequestFromProto(msg *agentcomposev2.RunAgentRequest) runs.RunAgentRequest {
	return runs.RunAgentRequest{
		ProjectID:        msg.GetProjectId(),
		AgentName:        msg.GetAgentName(),
		Prompt:           msg.GetPrompt(),
		Command:          msg.GetCommand(),
		Source:           api.ProjectRunSourceFromProto(msg.GetSource()),
		SchedulerID:      msg.GetSchedulerId(),
		TriggerID:        msg.GetTriggerId(),
		PayloadJSON:      msg.GetPayloadJson(),
		ClientRequestID:  msg.GetClientRequestId(),
		Env:              msg.GetEnv(),
		SandboxID:        msg.GetSandboxId(),
		Driver:           msg.GetDriver(),
		OutputSchemaJSON: msg.GetOutputSchemaJson(),
		CleanupPolicy:    msg.GetCleanupPolicy(),
		Jupyter:          msg.GetJupyter(),
		Volumes:          volumeMountSpecsFromProto(msg.GetVolumes()),
	}
}

func volumeMountSpecsFromProto(values []*agentcomposev2.VolumeMountSpec) []domain.VolumeMountSpec {
	out := make([]domain.VolumeMountSpec, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		out = append(out, domain.VolumeMountSpec{
			Type:     value.GetType(),
			Source:   value.GetSource(),
			Target:   value.GetTarget(),
			ReadOnly: value.GetReadOnly(),
		})
	}
	return out
}

func runConnectError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, runs.ErrInvalidRequest) {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	if errors.Is(err, domain.ErrUnsupported) ||
		errors.Is(err, domain.ErrNotFound) ||
		errors.Is(err, domain.ErrInvalidArgument) ||
		errors.Is(err, domain.ErrRequired) ||
		errors.Is(err, domain.ErrAmbiguous) ||
		errors.Is(err, domain.ErrFailedPrecondition) ||
		errors.Is(err, domain.ErrConflict) ||
		errors.Is(err, domain.ErrReferenced) ||
		errors.Is(err, domain.ErrAlreadyExists) {
		return api.ConnectErrorForDomain(err)
	}
	return connect.NewError(connect.CodeInternal, fmt.Errorf("%w", err))
}

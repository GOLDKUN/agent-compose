package api

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/runs"
	"agent-compose/pkg/sandboxes"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

type ExecSandboxStore interface {
	GetSandbox(context.Context, string) (*domain.Sandbox, error)
	GetVMState(string) (domain.VMState, error)
	ListSandboxSummaries(context.Context, []string) (map[string]domain.SandboxSummary, error)
}

type ExecProjectStore interface {
	GetProject(context.Context, string) (domain.ProjectRecord, error)
	GetProjectRun(context.Context, string) (domain.ProjectRunRecord, error)
	ListProjects(context.Context, domain.ProjectListOptions) (domain.ProjectListResult, error)
	ListProjectSandboxRuns(context.Context, domain.ProjectSandboxRelationFilter) ([]domain.ProjectRunRecord, error)
}

type ExecRuntime interface {
	ExecStream(context.Context, *domain.Sandbox, domain.VMState, domain.ExecSpec, domain.ExecStreamWriter) (domain.ExecResult, error)
}

type ExecInteractionRuntime interface {
	OpenInteraction(context.Context, *domain.Sandbox, domain.VMState, driverpkg.RuntimeStartSpec) (driverpkg.RuntimeInteraction, error)
}

type ExecRuntimeResolver func(*domain.Sandbox) (ExecRuntime, error)

type ExecRunAttachDelegate interface {
	RunProjectCommandAttach(context.Context, func() (*agentcomposev2.AttachAgentRunRequest, error), runs.RunAttachSender) error
}

type ExecHandler struct {
	config    *appconfig.Config
	store     ExecSandboxStore
	projects  ExecProjectStore
	runtime   ExecRuntimeResolver
	runAttach ExecRunAttachDelegate
	locks     *sandboxes.LifecycleLocks
}

func (h *ExecHandler) WithLifecycleLocks(locks *sandboxes.LifecycleLocks) *ExecHandler {
	h.locks = locks
	return h
}

// ExecHandlerDeps bundles NewExecHandler's required dependencies.
type ExecHandlerDeps struct {
	Config   *appconfig.Config
	Store    ExecSandboxStore
	Projects ExecProjectStore
	Runtime  ExecRuntimeResolver
}

func NewExecHandler(deps ExecHandlerDeps, runAttach ...ExecRunAttachDelegate) *ExecHandler {
	handler := &ExecHandler{
		config:   deps.Config,
		store:    deps.Store,
		projects: deps.Projects,
		runtime:  deps.Runtime,
	}
	if len(runAttach) > 0 {
		handler.runAttach = runAttach[0]
	}
	return handler
}

func (h *ExecHandler) Exec(ctx context.Context, req *connect.Request[agentcomposev2.ExecRequest]) (*connect.Response[agentcomposev2.ExecResponse], error) {
	result, err := h.executeProjectCommand(ctx, req.Msg, uuid.NewString(), nil)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&agentcomposev2.ExecResponse{Result: result}), nil
}

func (h *ExecHandler) StreamExec(ctx context.Context, req *connect.Request[agentcomposev2.ExecRequest], stream *connect.ServerStream[agentcomposev2.StreamExecResponse]) error {
	PrepareStreamingHeaders(stream.ResponseHeader())
	execID := uuid.NewString()
	result, err := h.executeProjectCommand(ctx, req.Msg, execID, func(resp *agentcomposev2.StreamExecResponse) error {
		return stream.Send(resp)
	})
	if err != nil {
		return err
	}
	return stream.Send(&agentcomposev2.StreamExecResponse{
		EventType: agentcomposev2.StreamExecEventType_STREAM_EXEC_EVENT_TYPE_COMPLETED,
		ExecId:    execID,
		SandboxId: result.GetSandboxId(),
		RunId:     result.GetRunId(),
		Result:    result,
	})
}

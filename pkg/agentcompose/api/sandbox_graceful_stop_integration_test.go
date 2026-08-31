package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/chaitin/agent-compose/pkg/identity"
	domain "github.com/chaitin/agent-compose/pkg/model"
	"github.com/chaitin/agent-compose/pkg/sandboxes"
	agentcomposev2 "github.com/chaitin/agent-compose/proto/agentcompose/v2"
	"github.com/chaitin/agent-compose/proto/agentcompose/v2/agentcomposev2connect"
)

func TestIntegrationGracefulStopPropagatesOptionsOverConnect(t *testing.T) {
	sandboxID := identity.NewID(identity.ResourceSandbox, "graceful-stop", "integration")
	running := &domain.Sandbox{Summary: domain.SandboxSummary{ID: sandboxID, VMStatus: domain.VMStatusRunning}}
	stopped := &domain.Sandbox{Summary: domain.SandboxSummary{ID: sandboxID, VMStatus: domain.VMStatusStopped}}
	delegate := &gracefulStopAPIDelegate{outcome: sandboxes.StopOutcome{
		Sandbox:       stopped,
		Preparation:   sandboxes.StopPreparationResult{Outcome: sandboxes.StopPreparationGraceful},
		DriverStopped: true,
	}}
	handler := NewSandboxHandler(SandboxHandlerDeps{
		Delegate:  delegate,
		Store:     &gracefulStopAPIStore{sandbox: running},
		Remover:   nil,
		Dashboard: nil,
	})
	servicePath, serviceHandler := agentcomposev2connect.NewSandboxServiceHandler(handler)
	mux := http.NewServeMux()
	mux.Handle(servicePath, serviceHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := agentcomposev2connect.NewSandboxServiceClient(server.Client(), server.URL)

	response, err := client.StopSandbox(context.Background(), connect.NewRequest(&agentcomposev2.StopSandboxRequest{
		SandboxId:   sandboxID,
		Mode:        agentcomposev2.SandboxStopMode_SANDBOX_STOP_MODE_GRACEFUL,
		GracePeriod: durationpb.New(12 * time.Second),
	}))
	if err != nil {
		t.Fatalf("StopSandbox() error = %v", err)
	}
	if response.Msg.GetOutcome() != agentcomposev2.SandboxStopOutcome_SANDBOX_STOP_OUTCOME_GRACEFUL ||
		response.Msg.GetSandbox().GetStatus() != agentcomposev2.SandboxStatus_SANDBOX_STATUS_STOPPED {
		t.Fatalf("StopSandbox() response = %#v", response.Msg)
	}
	if delegate.sandboxID != sandboxID || delegate.options != (sandboxes.StopOptions{Mode: sandboxes.StopModeGraceful, GracePeriod: 12 * time.Second}) {
		t.Fatalf("delegate call = id %q options %#v", delegate.sandboxID, delegate.options)
	}
}

type gracefulStopAPIDelegate struct {
	outcome   sandboxes.StopOutcome
	sandboxID string
	options   sandboxes.StopOptions
}

func (*gracefulStopAPIDelegate) ResumeSandbox(context.Context, string) (*domain.Sandbox, error) {
	return nil, nil
}

func (*gracefulStopAPIDelegate) StopSandbox(context.Context, string) (*domain.Sandbox, error) {
	return nil, nil
}

func (*gracefulStopAPIDelegate) GetSandboxProxy(context.Context, string) (SandboxProxy, error) {
	return SandboxProxy{}, nil
}

func (d *gracefulStopAPIDelegate) StopSandboxWithOptions(_ context.Context, sandboxID string, options sandboxes.StopOptions) (sandboxes.StopOutcome, error) {
	d.sandboxID = sandboxID
	d.options = options
	return d.outcome, nil
}

type gracefulStopAPIStore struct {
	sandbox *domain.Sandbox
}

func (s *gracefulStopAPIStore) GetSandbox(context.Context, string) (*domain.Sandbox, error) {
	return s.sandbox, nil
}

func (*gracefulStopAPIStore) RemoveSandbox(context.Context, string) error {
	return nil
}

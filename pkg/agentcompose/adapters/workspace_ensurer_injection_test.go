package adapters

import (
	"context"
	"testing"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/workspaces"
)

type constructorWorkspaceEnsurer struct{}

var _ workspaces.WorkspaceEnsurer = (*constructorWorkspaceEnsurer)(nil)

func (*constructorWorkspaceEnsurer) Ensure(context.Context, *domain.Sandbox) error {
	return nil
}

func TestWorkspaceEnsurerConstructorDependencies(t *testing.T) {
	t.Parallel()

	ensurer := &constructorWorkspaceEnsurer{}
	bridge := NewSandboxRPCBridge(SandboxRPCBridgeDeps{
		Config:           nil,
		Store:            nil,
		ConfigDB:         nil,
		WorkspaceEnsurer: ensurer,
		Driver:           nil,
		Runtimes:         nil,
		Bus:              nil,
		Streams:          nil,
		Cap:              nil,
		CapTokens:        nil,
		Dashboard:        nil,
		AgentExecutor:    nil,
	})
	runner := NewSchedulerSandboxRunner(SchedulerSandboxRunnerDeps{
		Config:           nil,
		Store:            nil,
		ConfigDB:         nil,
		WorkspaceEnsurer: ensurer,
		Driver:           nil,
		Cap:              nil,
		VolumeResolver:   nil,
		Streams:          nil,
		Publisher:        nil,
		CapTokens:        nil,
		AgentExecutor:    nil,
	})

	if bridge.workspaceEnsurer != ensurer {
		t.Fatalf("SandboxRPCBridge workspace ensurer = %p, want %p", bridge.workspaceEnsurer, ensurer)
	}
	if got := bridge.sessionLifecycle().WorkspaceEnsurer; got != ensurer {
		t.Fatalf("Lifecycle workspace ensurer = %p, want %p", got, ensurer)
	}
	if runner.workspaceEnsurer != ensurer {
		t.Fatalf("SchedulerSandboxRunner workspace ensurer = %p, want %p", runner.workspaceEnsurer, ensurer)
	}
}

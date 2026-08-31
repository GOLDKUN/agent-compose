package api

import (
	"context"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

type SandboxRuntimeReconciler interface {
	ReconcileRuntimeState(context.Context, *domain.Sandbox) (*domain.Sandbox, error)
}

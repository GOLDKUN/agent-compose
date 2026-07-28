package sandboxes

import (
	"context"
	"fmt"
	"time"

	domain "agent-compose/pkg/model"

	"github.com/google/uuid"
)

type StoppedRuntimeStore interface {
	UpdateSandbox(context.Context, *domain.Sandbox) error
	AddEvent(context.Context, string, domain.SandboxEvent) error
}

type SandboxRuntimeReleaser interface {
	ReleaseSandboxRuntime(context.Context, *domain.Sandbox) error
}

type SandboxStopDriver interface {
	StopSandboxVM(context.Context, *domain.Sandbox) error
}

type StopRuntimeResult struct {
	Stopped  bool
	Released bool
}

func SandboxStoppedEventMessage(result StopRuntimeResult) string {
	if result.Released {
		return "sandbox stopped and runtime released"
	}
	return "sandbox stopped and runtime retained"
}

// StopSandboxRuntime applies the snapshotted stopped-runtime policy. The caller
// owns the per-sandbox lifecycle lock and supplies whether a driver stop still
// needs to be confirmed.
func StopSandboxRuntime(ctx context.Context, sandboxRoot string, store StoppedRuntimeStore, driver SandboxStopDriver, sandbox *domain.Sandbox, stopRequired bool) (StopRuntimeResult, error) {
	if sandbox == nil || store == nil || driver == nil {
		return StopRuntimeResult{}, fmt.Errorf("stopped runtime lifecycle is not configured")
	}
	policy := domain.EffectiveStoppedRuntimePolicy(sandbox)
	releaser, canRelease := driver.(SandboxRuntimeReleaser)
	if policy == domain.StoppedRuntimePolicyRemove && !canRelease {
		return StopRuntimeResult{}, fmt.Errorf("sandbox runtime release is not supported")
	}

	now := time.Now().UTC()
	if policy == domain.StoppedRuntimePolicyRemove && (domain.EffectiveStoppedRuntimeState(sandbox) != domain.StoppedRuntimeStateReleased || stopRequired) {
		if sandbox.StoppedRuntime == nil || sandbox.StoppedRuntime.State != domain.StoppedRuntimeStateReleasePending {
			sandbox.StoppedRuntime = &domain.StoppedRuntime{State: domain.StoppedRuntimeStateReleasePending, RequestedAt: now}
		} else {
			sandbox.StoppedRuntime.LastError = ""
		}
		if err := store.UpdateSandbox(ctx, sandbox); err != nil {
			return StopRuntimeResult{}, fmt.Errorf("persist stopped runtime release intent: %w", err)
		}
	}

	result := StopRuntimeResult{}
	if stopRequired {
		if err := driver.StopSandboxVM(ctx, sandbox); err != nil {
			return result, err
		}
		result.Stopped = true
	}
	if sandbox.Summary.VMStatus != domain.VMStatusStopped || result.Stopped {
		sandbox.Summary.VMStatus = domain.VMStatusStopped
		if err := store.UpdateSandbox(ctx, sandbox); err != nil {
			return result, fmt.Errorf("persist stopped sandbox: %w", err)
		}
	}
	if policy != domain.StoppedRuntimePolicyRemove || domain.EffectiveStoppedRuntimeState(sandbox) == domain.StoppedRuntimeStateReleased {
		return result, nil
	}

	if err := releaser.ReleaseSandboxRuntime(ctx, sandbox); err != nil {
		sandbox.StoppedRuntime.LastError = err.Error()
		_ = store.UpdateSandbox(ctx, sandbox)
		recordStoppedRuntimeEvent(ctx, store, sandbox.Summary.ID, "sandbox.runtime_release_failed", "error", "sandbox stopped but runtime release failed", now)
		return result, fmt.Errorf("release stopped sandbox runtime: %w", err)
	}
	if err := MarkSandboxRuntimeReleased(sandboxRoot, sandbox); err != nil {
		sandbox.StoppedRuntime.LastError = err.Error()
		_ = store.UpdateSandbox(ctx, sandbox)
		recordStoppedRuntimeEvent(ctx, store, sandbox.Summary.ID, "sandbox.runtime_release_failed", "error", "sandbox runtime was removed but ownership update failed", now)
		return result, fmt.Errorf("persist released runtime ownership: %w", err)
	}
	sandbox.StoppedRuntime.State = domain.StoppedRuntimeStateReleased
	sandbox.StoppedRuntime.ReleasedAt = time.Now().UTC()
	sandbox.StoppedRuntime.LastError = ""
	if err := store.UpdateSandbox(ctx, sandbox); err != nil {
		return result, fmt.Errorf("persist released runtime state: %w", err)
	}
	result.Released = true
	recordStoppedRuntimeEvent(ctx, store, sandbox.Summary.ID, "sandbox.runtime_released", "info", "stopped sandbox runtime released", sandbox.StoppedRuntime.ReleasedAt)
	return result, nil
}

func recordStoppedRuntimeEvent(ctx context.Context, store StoppedRuntimeStore, sandboxID, eventType, level, message string, now time.Time) {
	_ = store.AddEvent(ctx, sandboxID, domain.SandboxEvent{ID: uuid.NewString(), Type: eventType, Level: level, Message: message, CreatedAt: now})
}

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
	GetVMState(string) (domain.VMState, error)
}

type SandboxRuntimeReleaser interface {
	ReleaseSandboxRuntime(context.Context, *domain.Sandbox) error
}

type SandboxStopDriver interface {
	StopSandboxVM(context.Context, *domain.Sandbox) error
}

type StopRuntimeResult struct {
	Stopped       bool
	Released      bool
	RuntimeEvents []domain.SandboxEvent
}

func SandboxStoppedEventMessage(sandbox *domain.Sandbox, result StopRuntimeResult) string {
	if result.Released {
		return "sandbox stopped and runtime released"
	}
	if RuntimeReleaseIntentional(sandbox) {
		return "sandbox stopped; runtime release pending"
	}
	return "sandbox stopped and runtime retained"
}

// StopSandboxRuntime applies the snapshotted stopped-runtime policy. The caller
// owns the per-sandbox lifecycle lock; this function owns the decision about
// whether the latest runtime start still needs a confirmed driver stop.
func StopSandboxRuntime(ctx context.Context, sandboxRoot string, store StoppedRuntimeStore, driver SandboxStopDriver, sandbox *domain.Sandbox) (StopRuntimeResult, error) {
	if sandbox == nil || store == nil {
		return StopRuntimeResult{}, fmt.Errorf("stopped runtime lifecycle is not configured")
	}
	stopRequired, err := sandboxRuntimeStopRequired(store, sandbox)
	if err != nil {
		return StopRuntimeResult{}, err
	}
	policy := EffectiveStoppedRuntimePolicy(sandbox)
	if !stopRequired && policy == domain.StoppedRuntimePolicyRetain {
		return StopRuntimeResult{}, nil
	}
	if driver == nil {
		return StopRuntimeResult{}, fmt.Errorf("stopped runtime lifecycle is not configured")
	}
	releaser, canRelease := driver.(SandboxRuntimeReleaser)
	if policy == domain.StoppedRuntimePolicyRemove && !canRelease {
		return StopRuntimeResult{}, fmt.Errorf("sandbox runtime release is not supported")
	}

	now := time.Now().UTC()
	if policy == domain.StoppedRuntimePolicyRemove && (EffectiveStoppedRuntimeState(sandbox) != domain.StoppedRuntimeStateReleased || stopRequired) {
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
	if policy != domain.StoppedRuntimePolicyRemove || EffectiveStoppedRuntimeState(sandbox) == domain.StoppedRuntimeStateReleased {
		return result, nil
	}

	if err := releaser.ReleaseSandboxRuntime(ctx, sandbox); err != nil {
		sandbox.StoppedRuntime.LastError = err.Error()
		_ = store.UpdateSandbox(ctx, sandbox)
		result.RuntimeEvents = append(result.RuntimeEvents, recordStoppedRuntimeEvent(ctx, store, sandbox.Summary.ID, "sandbox.runtime_release_failed", "error", "sandbox stopped but runtime release failed", now))
		return result, fmt.Errorf("release stopped sandbox runtime: %w", err)
	}
	if err := MarkSandboxRuntimeReleased(sandboxRoot, sandbox); err != nil {
		sandbox.StoppedRuntime.LastError = err.Error()
		_ = store.UpdateSandbox(ctx, sandbox)
		result.RuntimeEvents = append(result.RuntimeEvents, recordStoppedRuntimeEvent(ctx, store, sandbox.Summary.ID, "sandbox.runtime_release_failed", "error", "sandbox runtime was removed but ownership update failed", now))
		return result, fmt.Errorf("persist released runtime ownership: %w", err)
	}
	sandbox.StoppedRuntime.State = domain.StoppedRuntimeStateReleased
	sandbox.StoppedRuntime.ReleasedAt = time.Now().UTC()
	sandbox.StoppedRuntime.LastError = ""
	if err := store.UpdateSandbox(ctx, sandbox); err != nil {
		return result, fmt.Errorf("persist released runtime state: %w", err)
	}
	result.Released = true
	result.RuntimeEvents = append(result.RuntimeEvents, recordStoppedRuntimeEvent(ctx, store, sandbox.Summary.ID, "sandbox.runtime_released", "info", "stopped sandbox runtime released", sandbox.StoppedRuntime.ReleasedAt))
	return result, nil
}

func sandboxRuntimeStopRequired(store StoppedRuntimeStore, sandbox *domain.Sandbox) (bool, error) {
	if sandbox.Summary.VMStatus == domain.VMStatusRunning {
		return true, nil
	}
	vmState, err := store.GetVMState(sandbox.Summary.ID)
	if err != nil {
		return false, fmt.Errorf("load sandbox VM state before stop: %w", err)
	}
	latestStartRecorded := !vmState.StartedAt.IsZero() || !vmState.StartAttemptedAt.IsZero()
	return latestStartRecorded && !runtimeStopIsCurrent(vmState), nil
}

func recordStoppedRuntimeEvent(ctx context.Context, store StoppedRuntimeStore, sandboxID, eventType, level, message string, now time.Time) domain.SandboxEvent {
	event := domain.SandboxEvent{ID: uuid.NewString(), Type: eventType, Level: level, Message: message, CreatedAt: now}
	// Event persistence is best effort, matching the surrounding sandbox
	// lifecycle events. Returning the event still lets live subscribers observe
	// the transition if the history append cannot be completed.
	_ = store.AddEvent(ctx, sandboxID, event)
	return event
}

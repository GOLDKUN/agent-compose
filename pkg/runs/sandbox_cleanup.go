package runs

import (
	"context"
	"fmt"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sandboxes"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func (c *Controller) cleanupProjectRunSandboxByPolicy(ctx context.Context, sandboxResult SandboxResult, policy agentcomposev2.RunSandboxCleanupPolicy) error {
	sandbox := sandboxResult.Sandbox
	if CleanupPolicyRemovesSandbox(policy) && sandboxResult.Created {
		if c.removal != nil {
			result, err := c.removal.Remove(ctx, sandbox.Summary.ID, true)
			if err == nil && result.Removed && c.capTokens != nil {
				c.capTokens.RevokeSandbox(sandbox.Summary.ID)
			}
			return err
		}
		if err := c.stopProjectRunSandbox(ctx, sandbox); err != nil {
			return err
		}
		if c.store == nil {
			return fmt.Errorf("sandbox store is required")
		}
		if c.driver == nil {
			return fmt.Errorf("sandbox driver is required")
		}
		if err := c.driver.RemoveSandboxVM(ctx, sandbox); err != nil {
			return err
		}
		if err := c.store.RemoveSandbox(ctx, sandbox.Summary.ID); err != nil {
			return err
		}
		if c.dashboard != nil {
			c.dashboard.Notify("sandbox_removed")
		}
		return nil
	}
	return c.stopProjectRunSandbox(ctx, sandbox)
}

func (c *Controller) stopProjectRunSandbox(ctx context.Context, sandbox *domain.Sandbox) error {
	lifecycle, err := c.stopLifecycle()
	if err != nil {
		return err
	}
	_, err = lifecycle.StopLoaded(ctx, sandbox)
	return err
}

// stopProjectRunSandboxLocked stops a project-run sandbox while the caller
// owns its lifecycle lock. Keeping the locked form explicit avoids re-entering
// LifecycleLocks from sticky-binding retirement.
func (c *Controller) stopProjectRunSandboxLocked(ctx context.Context, sandbox *domain.Sandbox) error {
	lifecycle, err := c.stopLifecycle()
	if err != nil {
		return err
	}
	_, err = lifecycle.StopLoadedWhileLocked(ctx, sandbox)
	return err
}

func (c *Controller) stopLifecycle() (sandboxes.Lifecycle, error) {
	if c.store == nil {
		return sandboxes.Lifecycle{}, fmt.Errorf("sandbox store is required")
	}
	lifecycleStore, ok := c.store.(sandboxes.LifecycleStore)
	if !ok {
		return sandboxes.Lifecycle{}, fmt.Errorf("sandbox lifecycle store is required")
	}
	if c.driver == nil {
		return sandboxes.Lifecycle{}, fmt.Errorf("sandbox driver is required")
	}
	return sandboxes.Lifecycle{
		Config:        c.config,
		Store:         lifecycleStore,
		Driver:        c.driver,
		AccessRevoker: c.capTokens,
		Notifier: runSandboxLifecycleNotifier{
			streams:   c.streams,
			dashboard: c.dashboard,
		},
		Locks: c.lifecycleLocks,
	}, nil
}

type runSandboxLifecycleNotifier struct {
	streams   *sandboxes.StreamBroker
	dashboard DashboardNotifier
}

func (n runSandboxLifecycleNotifier) PublishSandboxUpdated(summary *domain.SandboxSummary) {
	if n.streams != nil {
		n.streams.PublishSandboxUpdated(summary)
	}
}

func (n runSandboxLifecycleNotifier) PublishEventAdded(sandboxID string, event domain.SandboxEvent) {
	if n.streams != nil {
		n.streams.PublishEventAdded(sandboxID, event)
	}
}

func (n runSandboxLifecycleNotifier) NotifyDashboard(reason string) {
	if n.dashboard != nil {
		n.dashboard.Notify(reason)
	}
}

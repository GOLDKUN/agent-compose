package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"agent-compose/pkg/cleanup"
	domain "agent-compose/pkg/model"
)

type WorkspaceCleanupStore interface {
	ListSandboxes(context.Context, domain.SandboxListOptions) (domain.SandboxListResult, error)
	GetSandbox(context.Context, string) (*domain.Sandbox, error)
	GetVMState(string) (domain.VMState, error)
	UpdateSandbox(context.Context, *domain.Sandbox) error
	AddEvent(context.Context, string, domain.SandboxEvent) error
	SandboxDir(string) string
}

type WorkspaceCleaner struct {
	Store       WorkspaceCleanupStore
	Locks       *LifecycleLocks
	ArchiveRoot string
	SandboxRoot string
	Removal     interface {
		Remove(context.Context, string, bool) (RemovalResult, error)
	}
	Now func() time.Time
}

func (c *WorkspaceCleaner) Name() string { return "sandbox-workspace" }

func (c *WorkspaceCleaner) Clean(ctx context.Context, cutoff time.Time) (cleanup.Result, error) {
	if c == nil || c.Store == nil {
		return cleanup.Result{}, fmt.Errorf("workspace cleaner store is not configured")
	}
	if strings.TrimSpace(c.ArchiveRoot) != "" {
		if _, err := validateSandboxArchiveRoot(c.ArchiveRoot, c.SandboxRoot); err != nil {
			return cleanup.Result{}, fmt.Errorf("validate sandbox archive root: %w", err)
		}
	}
	listed, err := c.Store.ListSandboxes(ctx, domain.SandboxListOptions{Limit: 1 << 30})
	if err != nil {
		return cleanup.Result{}, err
	}
	result := cleanup.Result{}
	var joined error
	for _, sandbox := range listed.Sandboxes {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(joined, err)
		}
		matched, removed, err := c.cleanSandbox(ctx, sandbox.Summary.ID, cutoff)
		if err != nil {
			if matched {
				result.Matched++
			}
			if removed {
				result.Removed++
			}
			result.Failed++
			joined = errors.Join(joined, fmt.Errorf("reclaim workspace for sandbox %s: %w", sandbox.Summary.ID, err))
			continue
		}
		if !matched {
			result.Skipped++
			continue
		}
		result.Matched++
		if removed {
			result.Removed++
		}
	}
	return result, joined
}

func (c *WorkspaceCleaner) cleanSandbox(ctx context.Context, sandboxID string, cutoff time.Time) (bool, bool, error) {
	unlock := c.Locks.Lock(sandboxID)
	matched, removed, err := c.cleanSandboxWhileLocked(ctx, sandboxID, cutoff)
	unlock()
	if err != nil || !matched || c.Removal == nil {
		return matched, removed, err
	}
	sandbox, loadErr := c.Store.GetSandbox(ctx, sandboxID)
	if loadErr != nil {
		return matched, removed, loadErr
	}
	if sandbox.Archive == nil || sandbox.Archive.State != domain.SandboxArchiveStateArchived {
		return matched, removed, nil
	}
	if _, err := validateCommittedSandboxArchive(
		ctx, c.ArchiveRoot, c.SandboxRoot, sandbox.Summary.ID, sandbox.Archive.ID,
	); err != nil {
		return matched, removed, fmt.Errorf("verify committed sandbox archive: %w", err)
	}
	result, removeErr := c.Removal.Remove(ctx, sandboxID, true)
	return matched, result.Removed, removeErr
}

func (c *WorkspaceCleaner) cleanSandboxWhileLocked(ctx context.Context, sandboxID string, cutoff time.Time) (bool, bool, error) {
	sandbox, err := c.Store.GetSandbox(ctx, sandboxID)
	if err != nil {
		return false, false, err
	}
	// Archival is the point of no return. If formal removal previously failed,
	// keep driving it even though its durable intent changed the VM status from
	// stopped to deleting.
	if sandbox.Archive != nil && sandbox.Archive.State == domain.SandboxArchiveStateArchived {
		return true, false, nil
	}
	eligibleAt, ok, err := c.workspaceEligibleAt(sandbox)
	if err != nil || !ok || eligibleAt.After(cutoff) {
		return false, false, err
	}

	matched := false
	removed := false
	var joined error
	if sandbox.WorkspaceReclamation == nil || sandbox.WorkspaceReclamation.State != domain.SandboxWorkspaceReclamationStateReclaimed {
		matched = true
		workspaceRemoved, reclaimErr := c.reclaimWorkspace(ctx, sandbox)
		removed = workspaceRemoved
		joined = errors.Join(joined, reclaimErr)
	}
	workspaceReclaimed := sandbox.WorkspaceReclamation != nil && sandbox.WorkspaceReclamation.State == domain.SandboxWorkspaceReclamationStateReclaimed
	if joined == nil && workspaceReclaimed && strings.TrimSpace(c.ArchiveRoot) != "" && (sandbox.Archive == nil || sandbox.Archive.State != domain.SandboxArchiveStateArchived) {
		matched = true
		joined = errors.Join(joined, c.archiveSandbox(ctx, sandbox))
	}
	return matched, removed, joined
}

func (c *WorkspaceCleaner) reclaimWorkspace(ctx context.Context, sandbox *domain.Sandbox) (bool, error) {
	retrying := sandbox.WorkspaceReclamation != nil && sandbox.WorkspaceReclamation.State == domain.SandboxWorkspaceReclamationStateReclaiming
	if sandbox.WorkspaceReclamation != nil && !retrying {
		return false, fmt.Errorf("unknown workspace reclamation state %q", sandbox.WorkspaceReclamation.State)
	}
	if !retrying {
		if _, err := c.safeWorkspacePath(sandbox); err != nil {
			return false, err
		}
		sandbox.WorkspaceReclamation = &domain.SandboxWorkspaceReclamation{
			State: domain.SandboxWorkspaceReclamationStateReclaiming, StartedAt: c.now(),
		}
		if err := c.Store.UpdateSandbox(ctx, sandbox); err != nil {
			return false, fmt.Errorf("persist reclamation intent: %w", err)
		}
	}
	workspacePath, err := c.safeWorkspacePath(sandbox)
	if err == nil {
		err = os.RemoveAll(workspacePath)
	}
	if err != nil {
		sandbox.WorkspaceReclamation.LastError = err.Error()
		_ = c.Store.UpdateSandbox(ctx, sandbox)
		return false, err
	}
	sandbox.WorkspaceReclamation.State = domain.SandboxWorkspaceReclamationStateReclaimed
	sandbox.WorkspaceReclamation.CompletedAt = c.now()
	sandbox.WorkspaceReclamation.LastError = ""
	if err := c.Store.UpdateSandbox(ctx, sandbox); err != nil {
		return false, fmt.Errorf("persist reclaimed workspace: %w", err)
	}
	_ = c.Store.AddEvent(ctx, sandbox.Summary.ID, domain.SandboxEvent{
		ID: uuid.NewString(), Type: "sandbox.workspace_reclaimed", Level: "info",
		Message: "sandbox workspace was reclaimed by retention policy", CreatedAt: c.now(),
	})
	return true, nil
}

func (c *WorkspaceCleaner) workspaceEligibleAt(sandbox *domain.Sandbox) (time.Time, bool, error) {
	if sandbox == nil {
		return time.Time{}, false, nil
	}
	if sandbox.Summary.VMStatus != domain.VMStatusStopped {
		return time.Time{}, false, nil
	}
	vmState, err := c.Store.GetVMState(sandbox.Summary.ID)
	if err != nil {
		return time.Time{}, false, err
	}
	if vmState.StoppedAt.IsZero() {
		return time.Time{}, false, nil
	}
	if !runtimeStopIsCurrent(vmState) {
		return time.Time{}, false, nil
	}
	return vmState.StoppedAt.UTC(), true, nil
}

func (c *WorkspaceCleaner) safeWorkspacePath(sandbox *domain.Sandbox) (string, error) {
	expected, err := filepath.Abs(filepath.Join(c.Store.SandboxDir(sandbox.Summary.ID), "workspace"))
	if err != nil {
		return "", err
	}
	actual, err := filepath.Abs(strings.TrimSpace(sandbox.Summary.WorkspacePath))
	if err != nil {
		return "", err
	}
	if actual != expected {
		return "", fmt.Errorf("workspace path %q is outside its authoritative sandbox", actual)
	}
	info, err := os.Lstat(actual)
	if os.IsNotExist(err) {
		return actual, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("workspace path %q is not a safe directory", actual)
	}
	return actual, nil
}

func (c *WorkspaceCleaner) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

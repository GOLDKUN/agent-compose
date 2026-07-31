package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"agent-compose/pkg/cleanup"
	domain "agent-compose/pkg/model"
)

// SandboxRetentionCleaner archives and removes stopped sandboxes after their
// retention period. Workspace cleanup remains an independent policy.
type SandboxRetentionCleaner struct {
	Store       WorkspaceCleanupStore
	Locks       *LifecycleLocks
	ArchiveRoot string
	SandboxRoot string
	Removal     interface {
		Remove(context.Context, string, bool) (RemovalResult, error)
	}
	Now func() time.Time
}

func (c *SandboxRetentionCleaner) Name() string { return "sandbox-retention" }

func (c *SandboxRetentionCleaner) Clean(ctx context.Context, cutoff time.Time) (cleanup.Result, error) {
	if c == nil || c.Store == nil {
		return cleanup.Result{}, fmt.Errorf("sandbox retention cleaner store is not configured")
	}
	if strings.TrimSpace(c.ArchiveRoot) == "" {
		return cleanup.Result{}, fmt.Errorf("sandbox retention archive root is not configured")
	}
	if _, err := validateSandboxArchiveRoot(c.ArchiveRoot, c.SandboxRoot); err != nil {
		return cleanup.Result{}, fmt.Errorf("validate sandbox archive root: %w", err)
	}
	ids, err := listCleanupSandboxIDs(ctx, c.Store)
	if err != nil {
		return cleanup.Result{}, err
	}
	result := cleanup.Result{}
	var joined error
	for _, sandboxID := range ids {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(joined, err)
		}
		matched, removed, err := c.cleanSandbox(ctx, sandboxID, cutoff)
		if err != nil {
			if matched {
				result.Matched++
			}
			if removed {
				result.Removed++
			}
			result.Failed++
			joined = errors.Join(joined, fmt.Errorf("retain sandbox %s: %w", sandboxID, err))
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

func (c *SandboxRetentionCleaner) cleanSandbox(ctx context.Context, sandboxID string, cutoff time.Time) (bool, bool, error) {
	unlock := c.Locks.Lock(sandboxID)
	matched, removed, err := c.cleanSandboxWhileLocked(ctx, sandboxID, cutoff)
	unlock()
	if err != nil || !matched || c.Removal == nil {
		return matched, removed, err
	}
	sandbox, loadErr := c.Store.GetSandbox(ctx, sandboxID)
	if loadErr != nil {
		if errors.Is(loadErr, os.ErrNotExist) {
			return matched, true, nil
		}
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

func (c *SandboxRetentionCleaner) cleanSandboxWhileLocked(ctx context.Context, sandboxID string, cutoff time.Time) (bool, bool, error) {
	sandbox, err := c.Store.GetSandbox(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, false, nil
		}
		return false, false, err
	}
	// A validated archive authorizes formal removal retries even after the
	// deletion journal changes the sandbox status from stopped to deleting.
	if sandbox.Archive != nil && sandbox.Archive.State == domain.SandboxArchiveStateArchived {
		return true, false, nil
	}
	workspaceCleaner := c.workspaceCleaner()
	eligibleAt, ok, err := workspaceCleaner.workspaceEligibleAt(sandbox)
	if err != nil || !ok || eligibleAt.After(cutoff) {
		return false, false, err
	}

	matched := false
	removed := false
	var joined error
	if sandbox.WorkspaceReclamation == nil || sandbox.WorkspaceReclamation.State != domain.SandboxWorkspaceReclamationStateReclaimed {
		matched = true
		workspaceRemoved, reclaimErr := workspaceCleaner.reclaimWorkspace(ctx, sandbox)
		removed = workspaceRemoved
		joined = errors.Join(joined, reclaimErr)
	}
	workspaceReclaimed := sandbox.WorkspaceReclamation != nil && sandbox.WorkspaceReclamation.State == domain.SandboxWorkspaceReclamationStateReclaimed
	if joined == nil && workspaceReclaimed && (sandbox.Archive == nil || sandbox.Archive.State != domain.SandboxArchiveStateArchived) {
		matched = true
		joined = errors.Join(joined, c.archiveSandbox(ctx, sandbox))
	}
	return matched, removed, joined
}

func (c *SandboxRetentionCleaner) workspaceCleaner() *WorkspaceCleaner {
	return &WorkspaceCleaner{Store: c.Store, Locks: c.Locks, Now: c.Now}
}

func (c *SandboxRetentionCleaner) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

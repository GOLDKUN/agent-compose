package sandboxstore

import (
	"context"
	"fmt"
	"sort"
	"strings"

	domain "agent-compose/pkg/model"
)

// listSandboxesFromFilesystem preserves the original listing contract for
// lightweight stores created through FromConfig, which intentionally do not
// own an index database lifecycle.
func (s *Store) listSandboxesFromFilesystem(ctx context.Context, options SandboxListOptions) (SandboxListResult, error) {
	locations, err := s.layout.discover()
	if err != nil {
		return SandboxListResult{}, fmt.Errorf("discover sandbox directories: %w", err)
	}
	var sandboxes []*Sandbox
	for _, location := range locations {
		if err := ctx.Err(); err != nil {
			return SandboxListResult{}, err
		}
		sandbox, err := s.loadSandboxFromDir(location.id, location.path)
		if err != nil {
			continue
		}
		s.hydrateSandboxGuestImage(sandbox)
		if !domain.SandboxMatchesListOptions(sandbox, options) || sandboxAtOrAfterCursor(sandbox, options) {
			continue
		}
		sandboxes = append(sandboxes, sandbox)
	}
	if projectID := strings.TrimSpace(options.ProjectID); projectID != "" {
		projectIDs, err := s.resolveSandboxProjectIDs(ctx, sandboxes)
		if err != nil {
			return SandboxListResult{}, fmt.Errorf("resolve sandbox project filter: %w", err)
		}
		filtered := sandboxes[:0]
		for _, sandbox := range sandboxes {
			if strings.EqualFold(projectIDs[sandbox.Summary.ID], projectID) {
				filtered = append(filtered, sandbox)
			}
		}
		sandboxes = filtered
	}
	sort.Slice(sandboxes, func(i, j int) bool {
		if sandboxes[i].Summary.UpdatedAt.Equal(sandboxes[j].Summary.UpdatedAt) {
			return sandboxes[i].Summary.ID > sandboxes[j].Summary.ID
		}
		return sandboxes[i].Summary.UpdatedAt.After(sandboxes[j].Summary.UpdatedAt)
	})
	total := len(sandboxes)
	offset, limit := domain.NormalizeSandboxListBounds(options.Offset, options.Limit)
	page := domain.PaginateSandboxes(sandboxes, offset, limit)
	nextOffset := min(offset+len(page), total)
	return SandboxListResult{
		Sandboxes:  page,
		TotalCount: total,
		HasMore:    nextOffset < total,
		NextOffset: nextOffset,
	}, nil
}

func sandboxAtOrAfterCursor(sandbox *Sandbox, options SandboxListOptions) bool {
	if options.BeforeUpdatedAt.IsZero() {
		return false
	}
	return sandbox.Summary.UpdatedAt.After(options.BeforeUpdatedAt) ||
		(sandbox.Summary.UpdatedAt.Equal(options.BeforeUpdatedAt) && sandbox.Summary.ID >= options.BeforeID)
}

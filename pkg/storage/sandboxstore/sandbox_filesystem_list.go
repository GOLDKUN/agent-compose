package sandboxstore

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/chaitin/agent-compose/pkg/sandboxes"
)

// listSandboxesFromFilesystem preserves the original listing contract for
// lightweight stores created through FromConfig, which intentionally do not
// own an index database lifecycle.
func (s *Store) listSandboxesFromFilesystem(ctx context.Context, options SandboxListOptions) (SandboxListResult, error) {
	locations, err := s.layout.discover()
	if err != nil {
		return SandboxListResult{}, fmt.Errorf("discover sandbox directories: %w", err)
	}
	var items []*Sandbox
	for _, location := range locations {
		if err := ctx.Err(); err != nil {
			return SandboxListResult{}, err
		}
		sandbox, err := s.loadSandboxFromDir(location.id, location.path)
		if err != nil {
			continue
		}
		s.hydrateSandboxGuestImage(sandbox)
		if !sandboxes.MatchesListOptions(sandbox, options) || sandboxAtOrAfterCursor(sandbox, options) {
			continue
		}
		items = append(items, sandbox)
	}
	if projectID := strings.TrimSpace(options.ProjectID); projectID != "" {
		projectIDs, err := s.resolveSandboxProjectIDs(ctx, items)
		if err != nil {
			return SandboxListResult{}, fmt.Errorf("resolve sandbox project filter: %w", err)
		}
		filtered := items[:0]
		for _, sandbox := range items {
			if strings.EqualFold(projectIDs[sandbox.Summary.ID], projectID) {
				filtered = append(filtered, sandbox)
			}
		}
		items = filtered
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Summary.UpdatedAt.Equal(items[j].Summary.UpdatedAt) {
			return items[i].Summary.ID > items[j].Summary.ID
		}
		return items[i].Summary.UpdatedAt.After(items[j].Summary.UpdatedAt)
	})
	total := len(items)
	offset, limit := sandboxes.NormalizeListBounds(options.Offset, options.Limit)
	page := sandboxes.Paginate(items, offset, limit)
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

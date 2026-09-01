package sandboxes

import (
	"context"
	"testing"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestListCleanupSandboxIDsUsesBoundedPages(t *testing.T) {
	store := &cleanupPagingLister{}
	ids, err := listCleanupSandboxIDs(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "first" || ids[1] != "second" {
		t.Fatalf("sandbox IDs = %v", ids)
	}
	if len(store.options) != 2 {
		t.Fatalf("ListSandboxes calls = %d, want 2", len(store.options))
	}
	if first := store.options[0]; first.Offset != 0 || first.Limit != cleanupSandboxPageSize {
		t.Fatalf("first page options = %#v", first)
	}
	if second := store.options[1]; second.Offset != 11 || second.Limit != cleanupSandboxPageSize {
		t.Fatalf("second page options = %#v", second)
	}
}

type cleanupPagingLister struct {
	options []domain.SandboxListOptions
}

func (s *cleanupPagingLister) ListSandboxes(_ context.Context, options domain.SandboxListOptions) (domain.SandboxListResult, error) {
	s.options = append(s.options, options)
	if options.Offset == 0 {
		return domain.SandboxListResult{
			Sandboxes:  []*domain.Sandbox{{Summary: domain.SandboxSummary{ID: "first"}}},
			HasMore:    true,
			NextOffset: 11,
		}, nil
	}
	return domain.SandboxListResult{Sandboxes: []*domain.Sandbox{{Summary: domain.SandboxSummary{ID: "second"}}}}, nil
}

package sandboxes

import (
	"context"
	"fmt"

	domain "agent-compose/pkg/model"
)

const cleanupSandboxPageSize = 200

type cleanupSandboxLister interface {
	ListSandboxes(context.Context, domain.SandboxListOptions) (domain.SandboxListResult, error)
}

// listCleanupSandboxIDs bounds full metadata loaded per request and finishes
// the stable listing pass before cleaners begin mutating or removing records.
func listCleanupSandboxIDs(ctx context.Context, store cleanupSandboxLister) ([]string, error) {
	var ids []string
	for offset := 0; ; {
		listed, err := store.ListSandboxes(ctx, domain.SandboxListOptions{
			Offset: offset,
			Limit:  cleanupSandboxPageSize,
		})
		if err != nil {
			return nil, err
		}
		for _, sandbox := range listed.Sandboxes {
			if sandbox != nil && sandbox.Summary.ID != "" {
				ids = append(ids, sandbox.Summary.ID)
			}
		}
		if !listed.HasMore {
			return ids, nil
		}
		if listed.NextOffset <= offset {
			return nil, fmt.Errorf("sandbox listing returned non-advancing offset %d after %d", listed.NextOffset, offset)
		}
		offset = listed.NextOffset
	}
}

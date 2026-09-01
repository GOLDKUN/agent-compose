package sandboxstore

import (
	"context"
	"strings"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

// SandboxProjectResolver supplies the domain-owned sandbox-to-project
// association used by the rebuildable listing projection.
type SandboxProjectResolver interface {
	ResolveSandboxProjectIDs(context.Context, []*domain.Sandbox) (map[string]string, error)
}

func (s *Store) resolveSandboxProjectIDs(ctx context.Context, sandboxes []*domain.Sandbox) (map[string]string, error) {
	if s.projectResolver == nil || len(sandboxes) == 0 {
		return map[string]string{}, nil
	}
	resolved, err := s.projectResolver.ResolveSandboxProjectIDs(ctx, sandboxes)
	if err != nil {
		return nil, err
	}
	for id, projectID := range resolved {
		resolved[id] = strings.TrimSpace(projectID)
	}
	return resolved, nil
}

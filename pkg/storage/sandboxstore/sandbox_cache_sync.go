package sandboxstore

import (
	"context"
	"fmt"
	"log/slog"
)

func (s *Store) recordIndex(session *Sandbox) {
	if s.index == nil || session == nil {
		return
	}
	indexed, err := s.loadSandbox(session.Summary.ID)
	if err != nil {
		s.indexDirty.Store(true)
		slog.Warn("load committed sandbox for index failed", "sandbox_id", session.Summary.ID, "error", err)
		return
	}
	indexCtx, cancel := context.WithTimeout(context.Background(), sandboxCacheWriteTimeout)
	defer cancel()
	projectIDs, err := s.resolveSandboxProjectIDs(indexCtx, []*Sandbox{indexed})
	if err != nil {
		s.indexDirty.Store(true)
		slog.Warn("resolve committed sandbox project for index failed", "sandbox_id", session.Summary.ID, "error", err)
		return
	}
	s.indexRepairMu.Lock()
	defer s.indexRepairMu.Unlock()
	if err := s.index.Upsert(indexCtx, indexed, projectIDs[indexed.Summary.ID]); err != nil {
		s.indexDirty.Store(true)
		slog.Warn("sandbox listing cache upsert failed", "sandbox_id", session.Summary.ID, "error", err)
	}
}

func (s *Store) deleteIndexRow(id string) error {
	if s.index == nil {
		return nil
	}
	s.indexRepairMu.Lock()
	defer s.indexRepairMu.Unlock()
	indexCtx, cancel := context.WithTimeout(context.Background(), sandboxCacheWriteTimeout)
	defer cancel()
	if err := s.index.Delete(indexCtx, id); err != nil {
		s.indexDirty.Store(true)
		return fmt.Errorf("delete sandbox listing cache row %s: %w", id, err)
	}
	return nil
}

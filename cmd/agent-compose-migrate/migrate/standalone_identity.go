package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

func loadDeletedStandaloneAgentIDs(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT d.id FROM agent_definition d
		LEFT JOIN project_agent a ON a.managed_agent_id=d.id
		WHERE a.id IS NULL AND d.deleted_at<>0 ORDER BY d.id`)
	if err != nil {
		return nil, fmt.Errorf("query deleted standalone agents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan deleted standalone agent: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func removeDeletedStandaloneAgents(ctx context.Context, tx *sql.Tx, ids []string) error {
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE loader SET agent_id='' WHERE agent_id=?`, id); err != nil {
			return fmt.Errorf("detach deleted standalone agent %s: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_definition WHERE id=?`, id); err != nil {
			return fmt.Errorf("remove deleted standalone agent %s: %w", id, err)
		}
	}
	return nil
}

func removeOnlyDeletedStandaloneAgents(ctx context.Context, db *sql.DB, ids []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin deleted standalone agent cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := removeDeletedStandaloneAgents(ctx, tx, ids); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit deleted standalone agent cleanup: %w", err)
	}
	return nil
}

func remapStandaloneAgentIdentities(ctx context.Context, tx *sql.Tx, agents []convertedStandaloneAgent) error {
	legacyIDs := make(map[string]struct{}, len(agents))
	nativeIDs := make(map[string]convertedStandaloneAgent, len(agents))
	for _, agent := range agents {
		legacyIDs[agent.definition.id] = struct{}{}
		if previous, exists := nativeIDs[agent.nativeID]; exists && previous.definition.id != agent.definition.id {
			return fmt.Errorf("standalone agents %s and %s map to duplicate native identity %s", previous.definition.id, agent.definition.id, agent.nativeID)
		}
		nativeIDs[agent.nativeID] = agent
	}
	for nativeID, planned := range nativeIDs {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT id FROM agent_definition WHERE id=?`, nativeID).Scan(&existing)
		if err == nil {
			if planned.reuseExisting && existing == nativeID {
				continue
			}
			if _, moving := legacyIDs[existing]; !moving {
				return fmt.Errorf("standalone agent %s native identity %s conflicts with an existing agent", planned.definition.id, nativeID)
			}
			continue
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check standalone agent identity %s: %w", nativeID, err)
		}
	}

	temporaryIDs := make(map[string]string, len(agents))
	for _, agent := range agents {
		if agent.reuseExisting {
			if err := remapLegacyAgentReferences(ctx, tx, agent.definition.id, agent.nativeID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM agent_definition WHERE id=?`, agent.definition.id); err != nil {
				return fmt.Errorf("remove reused standalone agent %s: %w", agent.definition.id, err)
			}
			continue
		}
		if agent.definition.id == agent.nativeID {
			continue
		}
		sum := sha256.Sum256([]byte(agent.definition.id))
		temporaryID := "legacy-remap-" + hex.EncodeToString(sum[:])
		if _, err := tx.ExecContext(ctx, `UPDATE agent_definition SET id=? WHERE id=?`, temporaryID, agent.definition.id); err != nil {
			return fmt.Errorf("stage standalone agent identity %s: %w", agent.definition.id, err)
		}
		if err := remapLegacyAgentReferences(ctx, tx, agent.definition.id, temporaryID); err != nil {
			return err
		}
		temporaryIDs[agent.definition.id] = temporaryID
	}
	for _, agent := range agents {
		if agent.reuseExisting {
			continue
		}
		currentID := agent.definition.id
		if temporaryID := strings.TrimSpace(temporaryIDs[currentID]); temporaryID != "" {
			currentID = temporaryID
		}
		if currentID == agent.nativeID {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_definition SET id=? WHERE id=?`, agent.nativeID, currentID); err != nil {
			return fmt.Errorf("assign standalone agent native identity %s: %w", agent.definition.id, err)
		}
		if err := remapLegacyAgentReferences(ctx, tx, currentID, agent.nativeID); err != nil {
			return err
		}
	}
	return nil
}

func remapLegacyAgentReferences(ctx context.Context, tx *sql.Tx, currentID, targetID string) error {
	for _, update := range []struct {
		operation string
		query     string
	}{
		{operation: "remap standalone loader agent reference", query: `UPDATE loader SET agent_id=? WHERE agent_id=?`},
		{operation: "remap standalone project run agent reference", query: `UPDATE project_run SET managed_agent_id=? WHERE managed_agent_id=?`},
	} {
		if _, err := tx.ExecContext(ctx, update.query, targetID, currentID); err != nil {
			return fmt.Errorf("%s %s: %w", update.operation, currentID, err)
		}
	}
	return nil
}

package migrate

import (
	"context"
	"database/sql"
	"fmt"
)

func detachOrphanLegacyEventSchedulers(ctx context.Context, db *sql.DB) ([]string, error) {
	tables := []string{"event_delivery", "event_sandbox_link", "event_session_link"}
	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		var exists int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
			return nil, fmt.Errorf("inspect legacy event scheduler table %s: %w", table, err)
		}
		if exists == 0 {
			continue
		}
		query := fmt.Sprintf(`SELECT COUNT(*) FROM %q AS item
			WHERE trim(item.loader_id)<>'' AND NOT EXISTS (SELECT 1 FROM loader WHERE loader.id=item.loader_id)`, table)
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return nil, fmt.Errorf("count orphan legacy event scheduler references in %s: %w", table, err)
		}
		counts[table] = count
	}
	total := 0
	for _, count := range counts {
		total += count
	}
	if total == 0 {
		return nil, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin orphan legacy event scheduler cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, table := range tables {
		if counts[table] == 0 {
			continue
		}
		query := fmt.Sprintf(`UPDATE %q AS item SET loader_id=''
			WHERE trim(item.loader_id)<>'' AND NOT EXISTS (SELECT 1 FROM loader WHERE loader.id=item.loader_id)`, table)
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return nil, fmt.Errorf("detach orphan legacy event scheduler references in %s: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit orphan legacy event scheduler cleanup: %w", err)
	}
	return []string{fmt.Sprintf(
		"detached %d orphan event delivery, %d sandbox link, and %d session link scheduler references",
		counts["event_delivery"], counts["event_sandbox_link"], counts["event_session_link"],
	)}, nil
}

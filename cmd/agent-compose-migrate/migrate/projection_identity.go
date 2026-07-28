package migrate

import (
	"context"
	"database/sql"
	"fmt"
)

func backfillLegacyProjectionIdentities(ctx context.Context, db *sql.DB) ([]string, error) {
	var agentCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_agent WHERE trim(id)=''`).Scan(&agentCount); err != nil {
		return nil, fmt.Errorf("count empty legacy project agent identities: %w", err)
	}
	var schedulerCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_scheduler WHERE trim(id)=''`).Scan(&schedulerCount); err != nil {
		return nil, fmt.Errorf("count empty legacy project scheduler identities: %w", err)
	}
	if agentCount == 0 && schedulerCount == 0 {
		return nil, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin legacy projection identity backfill: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE project_agent SET id=managed_agent_id WHERE trim(id)='' AND trim(managed_agent_id)<>''`); err != nil {
		return nil, fmt.Errorf("backfill legacy project agent identities: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project_scheduler SET id=scheduler_id WHERE trim(id)='' AND trim(scheduler_id)<>''`); err != nil {
		return nil, fmt.Errorf("backfill legacy project scheduler identities: %w", err)
	}
	var unresolved int
	if err := tx.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM project_agent WHERE trim(id)='') +
		(SELECT COUNT(*) FROM project_scheduler WHERE trim(id)='')`).Scan(&unresolved); err != nil {
		return nil, fmt.Errorf("verify legacy projection identity backfill: %w", err)
	}
	if unresolved != 0 {
		return nil, fmt.Errorf("legacy projection identity backfill left %d empty identities", unresolved)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit legacy projection identity backfill: %w", err)
	}
	return []string{fmt.Sprintf("backfilled %d project agent and %d project scheduler legacy identities", agentCount, schedulerCount)}, nil
}

func removeRetiredOrphanSchedulerProjections(ctx context.Context, db *sql.DB) ([]string, error) {
	var active int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_scheduler AS scheduler
		JOIN project ON project.id=scheduler.project_id
		LEFT JOIN loader ON loader.id=scheduler.managed_loader_id
		WHERE loader.id IS NULL AND project.removed_at=0`).Scan(&active); err != nil {
		return nil, fmt.Errorf("count active orphan scheduler projections: %w", err)
	}
	if active != 0 {
		return nil, fmt.Errorf("found %d active project scheduler projections whose legacy loader is missing", active)
	}
	var retired int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_scheduler AS scheduler
		JOIN project ON project.id=scheduler.project_id
		LEFT JOIN loader ON loader.id=scheduler.managed_loader_id
		WHERE loader.id IS NULL AND project.removed_at<>0`).Scan(&retired); err != nil {
		return nil, fmt.Errorf("count retired orphan scheduler projections: %w", err)
	}
	if retired == 0 {
		return nil, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin retired orphan scheduler cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE project_agent SET scheduler_enabled=0
		WHERE EXISTS (SELECT 1 FROM project_scheduler AS scheduler
			JOIN project ON project.id=scheduler.project_id
			LEFT JOIN loader ON loader.id=scheduler.managed_loader_id
			WHERE scheduler.project_id=project_agent.project_id
			  AND scheduler.agent_name=project_agent.agent_name
			  AND loader.id IS NULL AND project.removed_at<>0)`); err != nil {
		return nil, fmt.Errorf("disable retired orphan project schedulers: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_scheduler
		WHERE id IN (SELECT scheduler.id FROM project_scheduler AS scheduler
			JOIN project ON project.id=scheduler.project_id
			LEFT JOIN loader ON loader.id=scheduler.managed_loader_id
			WHERE loader.id IS NULL AND project.removed_at<>0)`); err != nil {
		return nil, fmt.Errorf("remove retired orphan scheduler projections: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit retired orphan scheduler cleanup: %w", err)
	}
	return []string{fmt.Sprintf("removed %d retired project scheduler projections whose legacy loader was missing", retired)}, nil
}

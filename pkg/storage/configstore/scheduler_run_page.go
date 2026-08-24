package configstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"agent-compose/pkg/idset"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/schedulers"
)

func (s *schedulerStore) GetSchedulerRunForSchedulers(ctx context.Context, schedulerIDs []string, runID string) (domain.SchedulerRunSummary, error) {
	schedulerIDs = idset.Normalize(schedulerIDs)
	runID = strings.TrimSpace(runID)
	if len(schedulerIDs) == 0 {
		return domain.SchedulerRunSummary{}, schedulerRunPageNotFound(runID, nil)
	}
	refClause, refArgs, ok := resourceIDClause("run_id", runID)
	if !ok {
		refClause = `run_id = ?`
		refArgs = []any{runID}
	}
	placeholders := make([]string, len(schedulerIDs))
	args := make([]any, 0, len(schedulerIDs)+len(refArgs))
	args = append(args, refArgs...)
	for index, schedulerID := range schedulerIDs {
		placeholders[index] = "?"
		args = append(args, schedulerID)
	}
	rows, err := s.db.QueryContext(ctx, schedulers.SelectSchedulerRunSQL()+` WHERE `+refClause+` AND scheduler_id IN (`+strings.Join(placeholders, ",")+`) AND trigger_id <> '' ORDER BY scheduler_id ASC, run_id ASC LIMIT 2`, args...)
	if err != nil {
		return domain.SchedulerRunSummary{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]domain.SchedulerRunSummary, 0, 2)
	for rows.Next() {
		item, scanErr := schedulers.ScanSchedulerRun(rows.Scan)
		if scanErr != nil {
			return domain.SchedulerRunSummary{}, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.SchedulerRunSummary{}, err
	}
	if len(items) == 0 {
		return domain.SchedulerRunSummary{}, schedulerRunPageNotFound(runID, sql.ErrNoRows)
	}
	if len(items) > 1 {
		return domain.SchedulerRunSummary{}, domain.ResourceError(domain.ErrAmbiguous, "scheduler run", runID, fmt.Sprintf("scheduler run reference %s is ambiguous", runID), nil)
	}
	return items[0], nil
}

func schedulerRunPageNotFound(runID string, cause error) error {
	return domain.ResourceError(domain.ErrNotFound, "scheduler run", runID, fmt.Sprintf("scheduler run %s not found", runID), cause)
}

func (s *schedulerStore) ListSchedulerRunsPage(ctx context.Context, filter schedulers.SchedulerRunPageFilter) ([]domain.SchedulerRunSummary, error) {
	schedulerIDs := idset.Normalize(filter.SchedulerIDs)
	if len(schedulerIDs) == 0 {
		return []domain.SchedulerRunSummary{}, nil
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	placeholders := make([]string, len(schedulerIDs))
	args := make([]any, 0, len(schedulerIDs)+7)
	for index, schedulerID := range schedulerIDs {
		placeholders[index] = "?"
		args = append(args, schedulerID)
	}
	query := schedulers.SelectSchedulerRunSQL() + ` WHERE scheduler_id IN (` + strings.Join(placeholders, ",") + `)`
	if filter.RequireTrigger {
		query += ` AND trigger_id <> ''`
	}
	if triggerID := strings.TrimSpace(filter.TriggerID); triggerID != "" {
		query += ` AND trigger_id = ?`
		args = append(args, triggerID)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	if !filter.BeforeStartedAt.IsZero() {
		query += ` AND (started_at < ? OR (started_at = ? AND (scheduler_id < ? OR (scheduler_id = ? AND run_id < ?))))`
		beforeMillis := filter.BeforeStartedAt.UTC().UnixMilli()
		args = append(args, beforeMillis, beforeMillis, strings.TrimSpace(filter.BeforeSchedulerID), strings.TrimSpace(filter.BeforeSchedulerID), strings.TrimSpace(filter.BeforeRunID))
	}
	query += ` ORDER BY started_at DESC, scheduler_id DESC, run_id DESC LIMIT ? OFFSET ?`
	args = append(args, filter.Limit, max(filter.Offset, 0))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query scheduler run page: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]domain.SchedulerRunSummary, 0)
	for rows.Next() {
		item, err := schedulers.ScanSchedulerRun(rows.Scan)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduler run page: %w", err)
	}
	return items, nil
}

func (s *schedulerStore) CountSchedulerRunsPage(ctx context.Context, filter schedulers.SchedulerRunPageFilter) (int, error) {
	schedulerIDs := idset.Normalize(filter.SchedulerIDs)
	if len(schedulerIDs) == 0 {
		return 0, nil
	}
	args := make([]any, 0, len(schedulerIDs)+2)
	for _, schedulerID := range schedulerIDs {
		args = append(args, schedulerID)
	}
	query := `SELECT COUNT(*) FROM scheduler_run WHERE scheduler_id IN (` + placeholders(len(schedulerIDs)) + `)`
	if filter.RequireTrigger {
		query += ` AND trigger_id <> ''`
	}
	if triggerID := strings.TrimSpace(filter.TriggerID); triggerID != "" {
		query += ` AND trigger_id = ?`
		args = append(args, triggerID)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count scheduler runs: %w", err)
	}
	return total, nil
}

func (s *schedulerStore) ListSchedulerRunSandboxIDs(ctx context.Context, keys []schedulers.SchedulerRunKey) (map[schedulers.SchedulerRunKey][]string, error) {
	result := make(map[schedulers.SchedulerRunKey][]string)
	clauses := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys)*2)
	seenKeys := make(map[schedulers.SchedulerRunKey]struct{}, len(keys))
	for _, key := range keys {
		key.SchedulerID = strings.TrimSpace(key.SchedulerID)
		key.RunID = strings.TrimSpace(key.RunID)
		if key.SchedulerID == "" || key.RunID == "" {
			continue
		}
		if _, ok := seenKeys[key]; ok {
			continue
		}
		seenKeys[key] = struct{}{}
		clauses = append(clauses, `(scheduler_id = ? AND scheduler_run_id = ?)`)
		args = append(args, key.SchedulerID, key.RunID)
	}
	if len(clauses) == 0 {
		return result, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT scheduler_id, scheduler_run_id, linked_sandbox_id FROM scheduler_event WHERE linked_sandbox_id <> '' AND (`+strings.Join(clauses, ` OR `)+`) ORDER BY scheduler_id ASC, scheduler_run_id ASC, linked_sandbox_id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("query scheduler run sandbox ids: %w", err)
	}
	defer func() { _ = rows.Close() }()
	seen := make(map[schedulers.SchedulerRunKey]map[string]struct{})
	for rows.Next() {
		var key schedulers.SchedulerRunKey
		var sandboxID string
		if err := rows.Scan(&key.SchedulerID, &key.RunID, &sandboxID); err != nil {
			return nil, fmt.Errorf("scan scheduler run sandbox id: %w", err)
		}
		if seen[key] == nil {
			seen[key] = make(map[string]struct{})
		}
		if _, ok := seen[key][sandboxID]; ok {
			continue
		}
		seen[key][sandboxID] = struct{}{}
		result[key] = append(result[key], sandboxID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduler run sandbox ids: %w", err)
	}
	return result, nil
}

func (s *schedulerStore) BatchGetLatestSchedulerRunsBySandboxIDs(ctx context.Context, schedulerIDs, sandboxIDs []string) (map[string]domain.SchedulerRunSummary, error) {
	schedulerIDs = idset.Normalize(schedulerIDs)
	sandboxIDs = idset.Normalize(sandboxIDs)
	result := make(map[string]domain.SchedulerRunSummary)
	if len(schedulerIDs) == 0 || len(sandboxIDs) == 0 {
		return result, nil
	}

	args := make([]any, 0, len(schedulerIDs)+len(sandboxIDs))
	for _, sandboxID := range sandboxIDs {
		args = append(args, sandboxID)
	}
	for _, schedulerID := range schedulerIDs {
		args = append(args, schedulerID)
	}
	query := `WITH associations AS (
		SELECT DISTINCT linked_sandbox_id AS sandbox_id, scheduler_id, scheduler_run_id AS run_id
		FROM scheduler_event
		WHERE linked_sandbox_id <> ''
			AND linked_sandbox_id IN (` + placeholders(len(sandboxIDs)) + `)
			AND scheduler_id IN (` + placeholders(len(schedulerIDs)) + `)
	), ranked AS (
		SELECT a.sandbox_id,
			r.scheduler_id, r.run_id, r.trigger_id, r.trigger_kind, r.trigger_source, r.status,
			r.started_at, r.completed_at, r.duration_ms, r.error, r.result_json, r.payload_json,
			r.source_script_sha256, r.artifacts_dir,
			ROW_NUMBER() OVER (
				PARTITION BY a.sandbox_id
				ORDER BY CASE WHEN r.completed_at > 0 THEN r.completed_at ELSE r.started_at END DESC,
					r.started_at DESC, r.scheduler_id DESC, r.run_id DESC
			) AS association_rank
		FROM associations a
		JOIN scheduler_run r ON r.scheduler_id = a.scheduler_id AND r.run_id = a.run_id
		WHERE r.trigger_id <> ''
	)
	SELECT sandbox_id, scheduler_id, run_id, trigger_id, trigger_kind, trigger_source, status,
		started_at, completed_at, duration_ms, error, result_json, payload_json,
		source_script_sha256, artifacts_dir
	FROM ranked
	WHERE association_rank = 1
	ORDER BY sandbox_id ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query latest scheduler runs by sandbox ids: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var sandboxID string
		run, scanErr := schedulers.ScanSchedulerRun(func(dest ...any) error {
			return rows.Scan(append([]any{&sandboxID}, dest...)...)
		})
		if scanErr != nil {
			return nil, scanErr
		}
		result[sandboxID] = run
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest scheduler runs by sandbox ids: %w", err)
	}
	return result, nil
}

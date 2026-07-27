package configstore

import (
	"context"
	"fmt"
	"strings"

	"agent-compose/pkg/loaders"
	domain "agent-compose/pkg/model"
)

func (s *loaderStore) ListLoaderEventsPage(ctx context.Context, filter loaders.LoaderEventPageFilter) ([]domain.LoaderEvent, error) {
	loaderIDs := normalizedLoaderRunPageIDs(filter.LoaderIDs)
	if len(loaderIDs) == 0 {
		return []domain.LoaderEvent{}, nil
	}
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	placeholders := make([]string, len(loaderIDs))
	args := make([]any, 0, len(loaderIDs)+10)
	for index, loaderID := range loaderIDs {
		placeholders[index] = "?"
		args = append(args, loaderID)
	}
	query := `SELECT e.loader_id, e.event_id, e.run_id, r.trigger_id, e.type, e.level, e.message, e.payload_json, e.linked_sandbox_id, e.linked_cell_id, e.linked_agent_thread_id, e.created_at
		FROM loader_event e JOIN loader_run r ON r.loader_id = e.loader_id AND r.run_id = e.run_id
		WHERE e.loader_id IN (` + strings.Join(placeholders, ",") + `)`
	if filter.RequireTrigger {
		query += ` AND r.trigger_id <> ''`
	}
	if triggerID := strings.TrimSpace(filter.TriggerID); triggerID != "" {
		query += ` AND r.trigger_id = ?`
		args = append(args, triggerID)
	}
	if runID := strings.TrimSpace(filter.RunID); runID != "" {
		query += ` AND r.run_id = ?`
		args = append(args, runID)
	}
	if !filter.BeforeCreatedAt.IsZero() {
		query += ` AND (e.created_at < ? OR (e.created_at = ? AND (e.loader_id < ? OR (e.loader_id = ? AND e.event_id < ?))))`
		beforeMillis := filter.BeforeCreatedAt.UTC().UnixMilli()
		args = append(args, beforeMillis, beforeMillis, strings.TrimSpace(filter.BeforeLoaderID), strings.TrimSpace(filter.BeforeLoaderID), strings.TrimSpace(filter.BeforeEventID))
	}
	if !filter.AfterCreatedAt.IsZero() {
		query += ` AND (e.created_at > ? OR (e.created_at = ? AND (e.loader_id > ? OR (e.loader_id = ? AND e.event_id > ?))))`
		afterMillis := filter.AfterCreatedAt.UTC().UnixMilli()
		args = append(args, afterMillis, afterMillis, strings.TrimSpace(filter.AfterLoaderID), strings.TrimSpace(filter.AfterLoaderID), strings.TrimSpace(filter.AfterEventID))
	}
	if !filter.FromCreatedAt.IsZero() {
		query += ` AND (e.created_at > ? OR (e.created_at = ? AND (e.loader_id > ? OR (e.loader_id = ? AND e.event_id >= ?))))`
		fromMillis := filter.FromCreatedAt.UTC().UnixMilli()
		args = append(args, fromMillis, fromMillis, strings.TrimSpace(filter.FromLoaderID), strings.TrimSpace(filter.FromLoaderID), strings.TrimSpace(filter.FromEventID))
	}
	if !filter.ThroughCreatedAt.IsZero() {
		query += ` AND (e.created_at < ? OR (e.created_at = ? AND (e.loader_id < ? OR (e.loader_id = ? AND e.event_id <= ?))))`
		throughMillis := filter.ThroughCreatedAt.UTC().UnixMilli()
		args = append(args, throughMillis, throughMillis, strings.TrimSpace(filter.ThroughLoaderID), strings.TrimSpace(filter.ThroughLoaderID), strings.TrimSpace(filter.ThroughEventID))
	}
	if filter.Ascending {
		query += ` ORDER BY e.created_at ASC, e.loader_id ASC, e.event_id ASC`
	} else {
		query += ` ORDER BY e.created_at DESC, e.loader_id DESC, e.event_id DESC`
	}
	query += ` LIMIT ?`
	args = append(args, filter.Limit)
	if filter.Offset > 0 {
		query += ` OFFSET ?`
		args = append(args, filter.Offset)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query loader event page: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]domain.LoaderEvent, 0)
	for rows.Next() {
		item, err := loaders.ScanLoaderEvent(rows.Scan)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate loader event page: %w", err)
	}
	return items, nil
}

func (s *loaderStore) CountLoaderEventsPage(ctx context.Context, filter loaders.LoaderEventPageFilter) (int, error) {
	loaderIDs := normalizedLoaderRunPageIDs(filter.LoaderIDs)
	if len(loaderIDs) == 0 {
		return 0, nil
	}
	args := make([]any, 0, len(loaderIDs)+2)
	for _, loaderID := range loaderIDs {
		args = append(args, loaderID)
	}
	query := `SELECT COUNT(*) FROM loader_event e JOIN loader_run r ON r.loader_id = e.loader_id AND r.run_id = e.run_id WHERE e.loader_id IN (` + placeholders(len(loaderIDs)) + `)`
	if filter.RequireTrigger {
		query += ` AND r.trigger_id <> ''`
	}
	if triggerID := strings.TrimSpace(filter.TriggerID); triggerID != "" {
		query += ` AND r.trigger_id = ?`
		args = append(args, triggerID)
	}
	if runID := strings.TrimSpace(filter.RunID); runID != "" {
		query += ` AND r.run_id = ?`
		args = append(args, runID)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count loader events: %w", err)
	}
	return total, nil
}

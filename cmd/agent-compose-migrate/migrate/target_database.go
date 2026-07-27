package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agent-compose/pkg/storage/sqlite"
)

type databaseCheckpoint func(warnings []string, schedulerIDs map[string]string, agentIDs map[string]standaloneAgentIdentity) error

func prepareTargetDatabase(
	ctx context.Context,
	db *sql.DB,
	sourceRoot, targetRoot string,
	resumeWarnings []string,
	resumeSchedulerIDs map[string]string,
	resumeAgentIDs map[string]standaloneAgentIdentity,
	checkpoint databaseCheckpoint,
) ([]string, map[string]string, map[string]standaloneAgentIdentity, error) {
	version, err := inspectVersion(ctx, db)
	if err != nil {
		return nil, nil, nil, err
	}
	if version == 0 {
		nonEmpty, err := applicationSchemaExists(ctx, db)
		if err != nil {
			return nil, nil, nil, err
		}
		if nonEmpty {
			if err := adoptUnversionedSchema(ctx, db); err != nil {
				return nil, nil, nil, err
			}
		}
		version, err = inspectVersion(ctx, db)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if version < 1 || version > 7 {
		return nil, nil, nil, fmt.Errorf("target database has an unknown migration version %d", version)
	}
	if err := validateVersionedPrefix(ctx, db, version); err != nil {
		return nil, nil, nil, fmt.Errorf("validate target migration history: %w", err)
	}
	if version < 4 {
		if err := sqlite.MigrateThrough(ctx, db, 4); err != nil {
			return nil, nil, nil, fmt.Errorf("advance target to conversion schema: %w", err)
		}
		version = 4
	}
	warnings := append([]string(nil), resumeWarnings...)
	agentIDs := cloneStandaloneAgentIdentities(resumeAgentIDs)
	if version == 4 {
		warnings, agentIDs, err = prepareLegacyProjectConversion(ctx, db, targetRoot, warnings, resumeSchedulerIDs, agentIDs, checkpoint)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	schedulerIDs := cloneSchedulerIDs(resumeSchedulerIDs)
	if schedulerIDs == nil {
		schedulerIDs, err = legacySchedulerIDMap(ctx, db, sourceRoot)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if checkpoint != nil {
		if err := checkpoint(warnings, schedulerIDs, agentIDs); err != nil {
			return nil, nil, nil, err
		}
	}
	if err := normalizeLegacyWorkspaceProviders(ctx, db); err != nil {
		return nil, nil, nil, err
	}
	if version < 6 {
		if err := rewriteLegacySchedulerArtifactPaths(ctx, db, sourceRoot, targetRoot, schedulerIDs); err != nil {
			return nil, nil, nil, err
		}
	}
	if version == 4 {
		orphanWarnings, orphanErr := detachOrphanLegacyEventSchedulers(ctx, db)
		if orphanErr != nil {
			return nil, nil, nil, orphanErr
		}
		warnings = append(warnings, orphanWarnings...)
		if checkpoint != nil && len(orphanWarnings) > 0 {
			if err := checkpoint(warnings, schedulerIDs, agentIDs); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	if err := sqlite.Migrate(ctx, db); err != nil {
		return nil, nil, nil, fmt.Errorf("migrate target database: %w", err)
	}
	linkWarnings, err := backfillProvableSchedulerRunLinks(ctx, db)
	if err != nil {
		return nil, nil, nil, err
	}
	warnings = append(warnings, linkWarnings...)
	pathWarnings, err := rewriteDataRootPaths(ctx, db, sourceRoot, targetRoot, schedulerIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	warnings = append(warnings, pathWarnings...)
	return warnings, schedulerIDs, agentIDs, nil
}

func backfillProvableSchedulerRunLinks(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT run_id FROM project_run
		WHERE source = 'scheduler' AND scheduler_run_id IS NULL ORDER BY run_id`)
	if err != nil {
		return nil, fmt.Errorf("list project runs missing scheduler links: %w", err)
	}
	var runIDs []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan project run missing scheduler link: %w", err)
		}
		runIDs = append(runIDs, runID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close project runs missing scheduler links: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project runs missing scheduler links: %w", err)
	}

	var warnings []string
	for _, runID := range runIDs {
		candidates, err := schedulerRunCandidates(ctx, db, runID)
		if err != nil {
			return nil, err
		}
		if len(candidates) != 1 {
			warnings = append(warnings, fmt.Sprintf("project run %s has %d provable scheduler run candidates; scheduler_run_id remains null", runID, len(candidates)))
			continue
		}
		if _, err := db.ExecContext(ctx, `UPDATE project_run SET scheduler_run_id=? WHERE run_id=? AND scheduler_run_id IS NULL`, candidates[0], runID); err != nil {
			return nil, fmt.Errorf("backfill project run %s scheduler link: %w", runID, err)
		}
	}
	return warnings, nil
}

func schedulerRunCandidates(ctx context.Context, db *sql.DB, projectRunID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT candidate.scheduler_run_id FROM (
		SELECT scheduler_event.scheduler_run_id
		FROM project_run
		JOIN project_scheduler ON project_scheduler.project_id = project_run.project_id
		 AND (project_scheduler.id = project_run.scheduler_id OR project_scheduler.scheduler_id = project_run.scheduler_id)
		JOIN scheduler_event ON scheduler_event.scheduler_id = project_scheduler.id
		 AND scheduler_event.linked_sandbox_id = project_run.sandbox_id
		WHERE project_run.run_id = ? AND project_run.sandbox_id <> '' AND scheduler_event.scheduler_run_id <> ''
		UNION
		SELECT event_sandbox_link.scheduler_run_id
		FROM project_run
		JOIN project_scheduler ON project_scheduler.project_id = project_run.project_id
		 AND (project_scheduler.id = project_run.scheduler_id OR project_scheduler.scheduler_id = project_run.scheduler_id)
		JOIN event_sandbox_link ON event_sandbox_link.scheduler_id = project_scheduler.id
		 AND event_sandbox_link.sandbox_id = project_run.sandbox_id
		WHERE project_run.run_id = ? AND project_run.sandbox_id <> '' AND event_sandbox_link.scheduler_run_id <> ''
	) AS candidate
	JOIN scheduler_run ON scheduler_run.run_id = candidate.scheduler_run_id
	ORDER BY candidate.scheduler_run_id`, projectRunID, projectRunID)
	if err != nil {
		return nil, fmt.Errorf("find scheduler run candidates for project run %s: %w", projectRunID, err)
	}
	defer func() { _ = rows.Close() }()
	var candidates []string
	for rows.Next() {
		var candidate string
		if err := rows.Scan(&candidate); err != nil {
			return nil, fmt.Errorf("scan scheduler run candidate for project run %s: %w", projectRunID, err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduler run candidates for project run %s: %w", projectRunID, err)
	}
	return candidates, nil
}

func legacySchedulerIDMap(ctx context.Context, db *sql.DB, sourceRoot string) (map[string]string, error) {
	columns, err := sqliteTableColumnTypes(ctx, db, "project_scheduler")
	if err != nil {
		return nil, err
	}
	if _, exists := columns["managed_loader_id"]; exists {
		return queryLegacySchedulerBridge(ctx, db)
	}
	return inferSchedulerIDMapFromArtifacts(ctx, db, sourceRoot)
}

func queryLegacySchedulerBridge(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT managed_loader_id, id FROM project_scheduler WHERE trim(managed_loader_id) <> ''`)
	if err != nil {
		return nil, fmt.Errorf("read legacy scheduler identity map: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make(map[string]string)
	for rows.Next() {
		var legacyID, schedulerID string
		if err := rows.Scan(&legacyID, &schedulerID); err != nil {
			return nil, fmt.Errorf("scan legacy scheduler identity map: %w", err)
		}
		result[legacyID] = schedulerID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy scheduler identity map: %w", err)
	}
	return result, nil
}

func inferSchedulerIDMapFromArtifacts(ctx context.Context, db *sql.DB, sourceRoot string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT scheduler_id, artifacts_dir FROM scheduler_run WHERE trim(artifacts_dir) <> ''`)
	if err != nil {
		return nil, fmt.Errorf("read scheduler artifact identity map: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make(map[string]string)
	for rows.Next() {
		var schedulerID, artifactsDir string
		if err := rows.Scan(&schedulerID, &artifactsDir); err != nil {
			return nil, fmt.Errorf("scan scheduler artifact identity map: %w", err)
		}
		path := strings.TrimSpace(artifactsDir)
		if filepath.IsAbs(path) {
			rel, relErr := filepath.Rel(sourceRoot, path)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			path = rel
		}
		parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
		if len(parts) < 2 || (parts[0] != "loaders" && parts[0] != "schedulers") {
			continue
		}
		if previous := result[parts[1]]; previous != "" && previous != schedulerID {
			return nil, fmt.Errorf("legacy scheduler artifact directory %s maps to multiple schedulers", parts[1])
		}
		result[parts[1]] = schedulerID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduler artifact identity map: %w", err)
	}
	return result, nil
}

func rewriteDataRootPaths(ctx context.Context, db *sql.DB, sourceRoot, targetRoot string, schedulerIDs map[string]string) ([]string, error) {
	sourceRoot, err := filepath.Abs(sourceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve source root for stored paths: %w", err)
	}
	targetRoot, err = filepath.Abs(targetRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve target root for stored paths: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin stored path rewrite: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var warnings []string
	for _, column := range []struct{ table, name string }{
		{table: "scheduler_run", name: "artifacts_dir"},
		{table: "project_run", name: "artifacts_dir"},
		{table: "project_run", name: "logs_path"},
		{table: "sandboxes", name: "workspace_path"},
	} {
		columnWarnings, rewriteErr := rewriteStoredPathColumn(ctx, tx, column.table, column.name, sourceRoot, targetRoot, schedulerIDs)
		if rewriteErr != nil {
			return nil, rewriteErr
		}
		warnings = append(warnings, columnWarnings...)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit stored path rewrite: %w", err)
	}
	return warnings, nil
}

func rewriteStoredPathColumn(ctx context.Context, tx *sql.Tx, table, column, sourceRoot, targetRoot string, schedulerIDs map[string]string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT rowid, %q FROM %q WHERE trim(%q) <> ''`, column, table, column))
	if err != nil {
		return nil, fmt.Errorf("read stored paths from %s.%s: %w", table, column, err)
	}
	type update struct {
		rowID int64
		path  string
	}
	var updates []update
	var warnings []string
	for rows.Next() {
		var rowID int64
		var stored string
		if err := rows.Scan(&rowID, &stored); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan stored path from %s.%s: %w", table, column, err)
		}
		rewritten, inside, rewriteErr := migratedStoredPath(stored, sourceRoot, targetRoot, schedulerIDs)
		if rewriteErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("rewrite stored path %s.%s row %d: %w", table, column, rowID, rewriteErr)
		}
		if !inside {
			if filepath.IsAbs(strings.TrimSpace(stored)) {
				warnings = append(warnings, fmt.Sprintf("left external path unchanged in %s.%s: %s", table, column, stored))
			}
			continue
		}
		if rewritten != stored {
			updates = append(updates, update{rowID: rowID, path: rewritten})
		}
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close stored paths from %s.%s: %w", table, column, err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stored paths from %s.%s: %w", table, column, err)
	}
	for _, item := range updates {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %q SET %q=? WHERE rowid=?`, table, column), item.path, item.rowID); err != nil {
			return nil, fmt.Errorf("update stored path in %s.%s: %w", table, column, err)
		}
	}
	return warnings, nil
}

func migratedStoredPath(stored, sourceRoot, targetRoot string, schedulerIDs map[string]string) (string, bool, error) {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return stored, false, nil
	}
	// Absolute paths are rewritten only when their ownership is proved by the
	// source or runtime root. A matching directory name elsewhere may be an
	// external volume and must remain untouched.
	if filepath.IsAbs(stored) {
		targetRelative, err := filepath.Rel(targetRoot, stored)
		if err == nil && targetRelative != ".." && !strings.HasPrefix(targetRelative, ".."+string(filepath.Separator)) {
			return filepath.Join(targetRoot, migratedDataRootPath(targetRelative, schedulerIDs)), true, nil
		}
		rel, err := filepath.Rel(sourceRoot, stored)
		if err != nil {
			return "", false, err
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.Join(targetRoot, migratedDataRootPath(rel, schedulerIDs)), true, nil
		}
		return stored, false, nil
	}
	// Data-root-relative paths have no absolute ownership boundary, so accept
	// only canonical paths whose first component is a known application root.
	if rel, ok := recognizedRelativeDataRootPath(stored); ok {
		return filepath.Join(targetRoot, migratedDataRootPath(rel, schedulerIDs)), true, nil
	}
	return stored, false, nil
}

func recognizedRelativeDataRootPath(stored string) (string, bool) {
	normalized := strings.ReplaceAll(filepath.Clean(strings.TrimSpace(stored)), `\`, "/")
	parts := strings.Split(normalized, "/")
	if len(parts) < 2 {
		return "", false
	}
	switch parts[0] {
	case "sessions", "sandboxes", "loaders", "schedulers":
	default:
		return "", false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	return filepath.FromSlash(strings.Join(parts, "/")), true
}

func verifyLatestTargetDatabase(ctx context.Context, path string) (int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, fmt.Errorf("inspect target database: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return 0, fmt.Errorf("target database must be a regular file")
	}
	db, err := openReadOnly(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	version, err := inspectVersion(ctx, db)
	if err != nil {
		return 0, err
	}
	if version != 7 {
		return 0, fmt.Errorf("resumable target database is schema v%d, want v7", version)
	}
	if err := validateVersionedPrefix(ctx, db, version); err != nil {
		return 0, fmt.Errorf("validate resumable target database: %w", err)
	}
	return version, nil
}

func adoptUnversionedSchema(ctx context.Context, db *sql.DB) error {
	baseline, ok, err := sqlite.EmbeddedMigrationAt(1)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("embedded baseline migration is unavailable")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin unversioned schema adoption: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := prepareLegacySchema(ctx, tx); err != nil {
		return fmt.Errorf("prepare unversioned schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, baseline.Statement); err != nil {
		return fmt.Errorf("apply embedded baseline to unversioned schema: %w", err)
	}
	if err := finalizeLegacySchema(ctx, tx); err != nil {
		return fmt.Errorf("finalize unversioned schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		checksum TEXT NOT NULL,
		applied_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER))
	)`); err != nil {
		return fmt.Errorf("create adopted migration history: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,checksum) VALUES(?,?,?)`, baseline.Version, baseline.Name, baseline.Checksum); err != nil {
		return fmt.Errorf("record adopted baseline: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit unversioned schema adoption: %w", err)
	}
	return nil
}

func applicationSchemaExists(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','index','trigger','view') AND name NOT LIKE 'sqlite_%'`).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect target application schema: %w", err)
	}
	return count > 0, nil
}

func validateUnversionedSource(ctx context.Context, db *sql.DB) error {
	nonEmpty, err := applicationSchemaExists(ctx, db)
	if err != nil || !nonEmpty {
		return err
	}
	var recognized int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('global_env','workspace_config','agent_definition','loader','project')`).Scan(&recognized); err != nil {
		return fmt.Errorf("inspect unversioned source shape: %w", err)
	}
	if recognized == 0 {
		return fmt.Errorf("unversioned source schema is not a recognized agent-compose layout")
	}
	return nil
}

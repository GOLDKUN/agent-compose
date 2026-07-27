package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

func rewriteLegacySchedulerArtifactPaths(
	ctx context.Context,
	db *sql.DB,
	sourceRoot, targetRoot string,
	schedulerIDs map[string]string,
) error {
	rows, err := db.QueryContext(ctx, `SELECT loader_id, run_id, artifacts_dir
		FROM loader_run WHERE trim(artifacts_dir) <> '' ORDER BY loader_id, run_id`)
	if err != nil {
		return fmt.Errorf("read legacy scheduler artifact paths: %w", err)
	}
	type artifactPath struct {
		loaderID, runID, stored string
	}
	var paths []artifactPath
	for rows.Next() {
		var item artifactPath
		if err := rows.Scan(&item.loaderID, &item.runID, &item.stored); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy scheduler artifact path: %w", err)
		}
		paths = append(paths, item)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy scheduler artifact paths: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy scheduler artifact paths: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy scheduler artifact path rewrite: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, item := range paths {
		schedulerID := strings.TrimSpace(schedulerIDs[item.loaderID])
		if schedulerID == "" {
			return fmt.Errorf("legacy scheduler %s has artifacts but no native identity mapping", item.loaderID)
		}
		targetPath := filepath.Join(targetRoot, "schedulers", schedulerID, "runs", item.runID)
		if !matchesLegacySchedulerArtifactPath(item.stored, sourceRoot, targetRoot, item.loaderID, schedulerID, item.runID) {
			return fmt.Errorf("legacy scheduler run %s artifact path %q is not a recognized data-root path", item.runID, item.stored)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE loader_run SET artifacts_dir=? WHERE loader_id=? AND run_id=?`, targetPath, item.loaderID, item.runID); err != nil {
			return fmt.Errorf("rewrite legacy scheduler run %s artifact path: %w", item.runID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy scheduler artifact path rewrite: %w", err)
	}
	return nil
}

func matchesLegacySchedulerArtifactPath(stored, sourceRoot, targetRoot, loaderID, schedulerID, runID string) bool {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return false
	}
	targetPath := filepath.Join(targetRoot, "schedulers", schedulerID, "runs", runID)
	if filepath.IsAbs(stored) && filepath.Clean(stored) == filepath.Clean(targetPath) {
		return true
	}
	normalized := strings.ReplaceAll(stored, `\`, "/")
	normalized = strings.TrimSuffix(normalized, "/")
	for _, suffix := range []string{
		filepath.ToSlash(filepath.Join("loaders", loaderID, "runs", runID)),
		filepath.ToSlash(filepath.Join("schedulers", schedulerID, "runs", runID)),
	} {
		if normalized == suffix || strings.HasSuffix(normalized, "/"+suffix) {
			return true
		}
	}
	if filepath.IsAbs(stored) {
		relative, err := filepath.Rel(sourceRoot, stored)
		return err == nil && (relative == filepath.Join("loaders", loaderID, "runs", runID) || relative == filepath.Join("schedulers", schedulerID, "runs", runID))
	}
	return false
}

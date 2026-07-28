package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	inPlaceBackupDatabase = "data.v1.db"
	inPlaceConvertedDB    = "data.v2.db"
	inPlaceOriginalDB     = "data.db.original"
	inPlaceOriginalWAL    = "data.db-wal.original"
	inPlaceOriginalSHM    = "data.db-shm.original"
	inPlaceJSONBackupRoot = "json"
	inPlaceJournalMode    = "in_place"
	inPlaceStagePrepared  = "prepared"
	inPlaceStageLayout    = "layout"
	inPlaceStageSwitch    = "switch"
)

func runInPlaceMigration(ctx context.Context, report Report, sourceDB *sql.DB, root, runtimeRoot string, progress io.Writer) (Report, error) {
	fail := func(stage string, err error) (Report, error) {
		report.Stage = stage
		report.Error = err.Error()
		return report, ErrReported
	}
	backupRoot := filepath.Join(root, inPlaceBackupName)
	if _, err := os.Stat(backupRoot); err == nil {
		return fail("validate", fmt.Errorf("in-place backup exists without a migration journal: %s", backupRoot))
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail("validate", fmt.Errorf("inspect in-place backup: %w", err))
	}
	if err := os.Mkdir(backupRoot, 0o700); err != nil {
		return fail("database", fmt.Errorf("create in-place backup: %w", err))
	}
	state := journal{
		Mode:          inPlaceJournalMode,
		SourceVersion: report.SourceVersion,
		RuntimeRoot:   runtimeRoot,
		Stage:         "database",
	}
	if err := writeJournal(root, state); err != nil {
		return fail("database", err)
	}
	if err := prepareInPlaceDatabases(ctx, root, runtimeRoot, sourceDB, &state); err != nil {
		return fail("database", err)
	}
	report.Warnings = append([]string(nil), state.Warnings...)
	return continueInPlaceMigration(ctx, report, root, runtimeRoot, state, progress)
}

func resumeInPlaceMigration(ctx context.Context, report Report, root, runtimeRoot string, progress io.Writer) (Report, error) {
	fail := func(stage string, err error) (Report, error) {
		report.Stage = stage
		report.Error = err.Error()
		return report, ErrReported
	}
	state, err := readJournal(root)
	if err != nil {
		return fail("validate", err)
	}
	if state.Mode != inPlaceJournalMode {
		return fail("validate", fmt.Errorf("existing migration journal is not an in-place migration"))
	}
	if err := validateInPlaceBackupRoot(filepath.Join(root, inPlaceBackupName)); err != nil {
		return fail("validate", err)
	}
	if filepath.Clean(state.RuntimeRoot) != filepath.Clean(runtimeRoot) {
		return fail("validate", fmt.Errorf("runtime root changed since the in-place migration started"))
	}
	report.SourceFingerprint = state.SourceFingerprint
	report.SourceVersion = state.SourceVersion
	report.Warnings = append([]string(nil), state.Warnings...)
	if state.Stage == "complete" {
		report.Stage = "complete"
		report.TargetVersion, err = verifyLatestTargetDatabase(ctx, filepath.Join(root, databaseName))
		if err != nil {
			return fail("complete", err)
		}
		return report, nil
	}
	if err := validateStoppedLegacySandboxes(root); err != nil {
		return fail("validate", err)
	}
	if state.Stage == "database" {
		if err := prepareInPlaceDatabases(ctx, root, runtimeRoot, nil, &state); err != nil {
			return fail("database", err)
		}
	}
	return continueInPlaceMigration(ctx, report, root, runtimeRoot, state, progress)
}

func prepareInPlaceDatabases(ctx context.Context, root, runtimeRoot string, sourceDB *sql.DB, state *journal) error {
	backupRoot := filepath.Join(root, inPlaceBackupName)
	backupDB := filepath.Join(backupRoot, inPlaceBackupDatabase)
	if _, err := os.Stat(backupDB); errors.Is(err, os.ErrNotExist) {
		if sourceDB == nil {
			snapshot, snapshotErr := openSourceDatabaseSnapshot(root)
			if snapshotErr != nil {
				return snapshotErr
			}
			defer func() { _ = snapshot.Close() }()
			sourceDB = snapshot.db
		}
		if err := snapshotDatabase(ctx, sourceDB, backupDB); err != nil {
			return fmt.Errorf("back up source database: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect source database backup: %w", err)
	}
	convertedDB := filepath.Join(backupRoot, inPlaceConvertedDB)
	if _, err := os.Stat(convertedDB); errors.Is(err, os.ErrNotExist) {
		backupSource, openErr := openReadOnly(backupDB)
		if openErr != nil {
			return fmt.Errorf("open source database backup: %w", openErr)
		}
		if err := snapshotDatabase(ctx, backupSource, convertedDB); err != nil {
			_ = backupSource.Close()
			return fmt.Errorf("create converted database: %w", err)
		}
		if err := backupSource.Close(); err != nil {
			return fmt.Errorf("close source database backup: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect converted database: %w", err)
	}
	db, err := sql.Open("sqlite", convertedDB)
	if err != nil {
		return fmt.Errorf("open converted database: %w", err)
	}
	db.SetMaxOpenConns(1)
	checkpoint := func(warnings []string, schedulerIDs map[string]string, agentIDs map[string]standaloneAgentIdentity) error {
		state.Warnings = append([]string(nil), warnings...)
		state.SchedulerIDs = cloneSchedulerIDs(schedulerIDs)
		state.AgentIDs = cloneStandaloneAgentIdentities(agentIDs)
		return writeJournal(root, *state)
	}
	warnings, schedulerIDs, agentIDs, err := prepareTargetDatabase(ctx, db, root, runtimeRoot, state.Warnings, state.SchedulerIDs, state.AgentIDs, checkpoint)
	if err != nil {
		_ = db.Close()
		return err
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close converted database: %w", err)
	}
	state.Warnings = append([]string(nil), warnings...)
	state.SchedulerIDs = cloneSchedulerIDs(schedulerIDs)
	state.AgentIDs = cloneStandaloneAgentIdentities(agentIDs)
	state.Stage = inPlaceStagePrepared
	return writeJournal(root, *state)
}

func continueInPlaceMigration(ctx context.Context, report Report, root, runtimeRoot string, state journal, progress io.Writer) (Report, error) {
	fail := func(stage string, err error) (Report, error) {
		report.Stage = stage
		report.Error = err.Error()
		return report, ErrReported
	}
	if state.Stage == inPlaceStagePrepared {
		writeMigrationProgress(progress, "files", "inspecting migration layout")
		files, err := inspectInPlaceAuthoritativeFiles(ctx, root, runtimeRoot, state.SchedulerIDs, state.AgentIDs, progress)
		if err != nil {
			return fail("layout", err)
		}
		report.CheckedFiles = files
		state.Stage = inPlaceStageLayout
		if err := writeJournal(root, state); err != nil {
			return fail("layout", err)
		}
	}
	if state.Stage == inPlaceStageLayout {
		writeMigrationProgress(progress, "layout", "applying in-place layout")
		if err := applyInPlaceLayout(root, runtimeRoot, state.SchedulerIDs, state.AgentIDs); err != nil {
			return fail("layout", err)
		}
		state.Stage = inPlaceStageSwitch
		if err := writeJournal(root, state); err != nil {
			return fail("switch", err)
		}
	}
	if state.Stage == inPlaceStageSwitch {
		writeMigrationProgress(progress, "database", "activating migrated database")
		if err := switchInPlaceDatabase(ctx, root); err != nil {
			return fail("switch", err)
		}
		state.Stage = "complete"
		state.Complete = true
		if err := writeJournal(root, state); err != nil {
			return fail("complete", err)
		}
	}
	report.Stage = "complete"
	report.TargetVersion = 7
	report.Warnings = append([]string(nil), state.Warnings...)
	writeMigrationProgress(progress, "complete", "in-place migration completed")
	return report, nil
}

func applyInPlaceLayout(root, runtimeRoot string, schedulerIDs map[string]string, agentIDs map[string]standaloneAgentIdentity) error {
	if err := rewriteInPlaceSymlinks(root, runtimeRoot, schedulerIDs); err != nil {
		return err
	}
	legacySchedulers := filepath.Join(root, "loaders")
	legacyIDs := make([]string, 0, len(schedulerIDs))
	for legacyID := range schedulerIDs {
		legacyIDs = append(legacyIDs, legacyID)
	}
	sort.Strings(legacyIDs)
	for _, legacyID := range legacyIDs {
		nativeID := strings.TrimSpace(schedulerIDs[legacyID])
		if err := validateInPlaceIdentity("legacy scheduler", legacyID); err != nil {
			return err
		}
		if err := validateInPlaceIdentity("native scheduler", nativeID); err != nil {
			return err
		}
		if nativeID == legacyID {
			continue
		}
		if err := renameInPlace(filepath.Join(legacySchedulers, legacyID), filepath.Join(legacySchedulers, nativeID)); err != nil {
			return fmt.Errorf("rename scheduler directory %s to %s: %w", legacyID, nativeID, err)
		}
	}
	if err := renameInPlace(filepath.Join(root, "loaders"), filepath.Join(root, "schedulers")); err != nil {
		return fmt.Errorf("rename loaders directory: %w", err)
	}
	if err := renameInPlace(filepath.Join(root, "sessions"), filepath.Join(root, "sandboxes")); err != nil {
		return fmt.Errorf("rename sessions directory: %w", err)
	}
	return rewriteInPlaceSandboxJSON(root, runtimeRoot, schedulerIDs, agentIDs)
}

func validateInPlaceIdentity(kind, value string) error {
	if value == "" || value == "." || value == ".." || filepath.Base(value) != value || strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("%s id %q is not a safe directory name", kind, value)
	}
	return nil
}

func validateInPlaceBackupRoot(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect in-place backup: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("in-place backup must be a directory, not a symlink")
	}
	return nil
}

func renameInPlace(source, target string) error {
	_, sourceErr := os.Lstat(source)
	_, targetErr := os.Lstat(target)
	if errors.Is(sourceErr, os.ErrNotExist) {
		if targetErr == nil || errors.Is(targetErr, os.ErrNotExist) {
			return nil
		}
		return targetErr
	}
	if sourceErr != nil {
		return sourceErr
	}
	if targetErr == nil {
		return fmt.Errorf("target already exists")
	}
	if !errors.Is(targetErr, os.ErrNotExist) {
		return targetErr
	}
	return os.Rename(source, target)
}

func rewriteInPlaceSandboxJSON(root, runtimeRoot string, schedulerIDs map[string]string, agentIDs map[string]standaloneAgentIdentity) error {
	sandboxRoot := filepath.Join(root, "sandboxes")
	if _, err := os.Stat(sandboxRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(sandboxRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if skipInPlacePayloadSubtree(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, handled, err := rewriteMigratedJSON(path, rel, root, runtimeRoot, schedulerIDs, agentIDs)
		if err != nil || !handled {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		backupPath := filepath.Join(root, inPlaceBackupName, inPlaceJSONBackupRoot, rel)
		if _, err := os.Stat(backupPath); errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
				return err
			}
			if err := copyFile(path, backupPath, info.Mode().Perm()); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		temporary := path + ".agent-compose-migrate.tmp"
		if err := os.WriteFile(temporary, data, info.Mode().Perm()); err != nil {
			return err
		}
		if err := os.Rename(temporary, path); err != nil {
			return err
		}
		return nil
	})
}

func switchInPlaceDatabase(ctx context.Context, root string) error {
	backupRoot := filepath.Join(root, inPlaceBackupName)
	current := filepath.Join(root, databaseName)
	converted := filepath.Join(backupRoot, inPlaceConvertedDB)
	if version, err := databaseVersionIfPresent(ctx, current); err != nil {
		return err
	} else if version == 7 {
		if err := os.Remove(converted); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove redundant converted database: %w", err)
		}
		return nil
	}
	if _, err := verifyLatestTargetDatabase(ctx, converted); err != nil {
		return fmt.Errorf("verify converted database before activation: %w", err)
	}
	if err := checkpointDatabaseForActivation(ctx, converted); err != nil {
		return fmt.Errorf("checkpoint converted database before activation: %w", err)
	}
	for _, item := range []struct{ source, backup string }{
		{source: current, backup: filepath.Join(backupRoot, inPlaceOriginalDB)},
		{source: current + "-wal", backup: filepath.Join(backupRoot, inPlaceOriginalWAL)},
		{source: current + "-shm", backup: filepath.Join(backupRoot, inPlaceOriginalSHM)},
	} {
		if _, err := os.Lstat(item.source); err == nil {
			if _, backupErr := os.Lstat(item.backup); errors.Is(backupErr, os.ErrNotExist) {
				if err := os.Rename(item.source, item.backup); err != nil {
					return fmt.Errorf("back up original database file %s: %w", filepath.Base(item.source), err)
				}
			} else if backupErr != nil {
				return backupErr
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if _, err := os.Stat(converted); err != nil {
		return fmt.Errorf("converted database is unavailable: %w", err)
	}
	if err := os.Rename(converted, current); err != nil {
		return fmt.Errorf("activate converted database: %w", err)
	}
	_, err := verifyLatestTargetDatabase(ctx, current)
	return err
}

func checkpointDatabaseForActivation(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	var busy, logFrames, checkpointedFrames int
	checkpointErr := db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointedFrames)
	closeErr := db.Close()
	if checkpointErr != nil {
		return errors.Join(checkpointErr, closeErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if busy != 0 || (logFrames >= 0 && logFrames != checkpointedFrames) {
		return fmt.Errorf("WAL checkpoint incomplete: busy=%d log=%d checkpointed=%d", busy, logFrames, checkpointedFrames)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove checkpointed database companion %s: %w", filepath.Base(path+suffix), err)
		}
	}
	return nil
}

func databaseVersionIfPresent(ctx context.Context, path string) (int64, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return -1, nil
	} else if err != nil {
		return 0, err
	}
	db, err := openReadOnly(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	return inspectVersion(ctx, db)
}

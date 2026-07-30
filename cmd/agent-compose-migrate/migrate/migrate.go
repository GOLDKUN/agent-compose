package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

const (
	databaseName         = "data.db"
	journalName          = ".agent-compose-migrate.json"
	inPlaceBackupName    = ".agent-compose-migrate-backup"
	currentSchemaVersion = 11
)

var knownMigrationChecksums = map[int64]string{
	1:  "6d2a07e2df01c38a57989accc3eb265cc3238ae3322f5dd540383235e59e27a9",
	2:  "fa328b0bd1be3620d4a92b94bd39d6cb4a3a6d454ce1de643e82598f9c028a49",
	3:  "5bdbd3258245ce7fc025121625408b35b444a425ffcb89aed0b8b7a846969183",
	4:  "3d5c2ab028a6f7e1c461f0af3fc6d898807b60159f1625b8939987d6ce7b91cb",
	5:  "e43cc1ffdfcd45e5e81a8fc098a6e77299e6e437f8c447eb0db49443a3bd29d2",
	6:  "92da2ea1c85e7d1321ca1e4260370a3d12057219005a83808529dbdd8d25299a",
	7:  "a8cb740e25992d3f3121bcfbff07c67cff699e8625d281edf60c0e76f91ce9ba",
	8:  "4569a4d7f82de70d3a2545dcdff07e0929e55daffaedf13fd6f2b8a8bcbf1d3e",
	9:  "916b84e78f6956ae5af9e38720fa4c6c0c7dfe9d72ddba068790ce44a80e7fb3",
	10: "63a1d45d94cbd1ade08ee556c7615f2dcbdd4411740f03ed12d93d67a1509d78",
	11: "8c3ef0d428da031391394df2313d581484f39f8e94a0e8b5435d9947e67ba6ef",
}

var ErrReported = errors.New("migration failure is included in the report")

type Options struct {
	Source      string
	Target      string
	RuntimeRoot string
	DryRun      bool
	JSON        bool
	Progress    io.Writer
}

func Run(ctx context.Context, options Options) (Report, error) {
	report := Report{Source: filepath.Clean(options.Source), Target: filepath.Clean(options.Target), Stage: "validate", DryRun: options.DryRun}
	fail := func(err error) (Report, error) {
		report.Error = err.Error()
		return report, ErrReported
	}
	if strings.TrimSpace(options.Source) == "" || strings.TrimSpace(options.Target) == "" {
		return fail(fmt.Errorf("--source and --target are required"))
	}
	source, err := filepath.Abs(options.Source)
	if err != nil {
		return fail(fmt.Errorf("resolve source: %w", err))
	}
	target, err := filepath.Abs(options.Target)
	if err != nil {
		return fail(fmt.Errorf("resolve target: %w", err))
	}
	report.Source, report.Target = source, target
	runtimeRoot := strings.TrimSpace(options.RuntimeRoot)
	if runtimeRoot == "" {
		runtimeRoot = target
	} else {
		runtimeRoot, err = filepath.Abs(runtimeRoot)
		if err != nil {
			return fail(fmt.Errorf("resolve runtime root: %w", err))
		}
	}
	if err := validateMigrationDataRoots(source, target); err != nil {
		return fail(err)
	}
	inPlace, err := sameDataRoot(source, target)
	if err != nil {
		return fail(err)
	}
	report.InPlace = inPlace
	if inPlace {
		report.Backup = filepath.Join(source, inPlaceBackupName)
	}
	if inPlace && !options.DryRun {
		if _, err := os.Stat(filepath.Join(source, journalName)); err == nil {
			return resumeInPlaceMigration(ctx, report, source, runtimeRoot, options.Progress)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail(fmt.Errorf("inspect in-place migration journal: %w", err))
		}
	}
	if err := validateSourceRoot(source); err != nil {
		return fail(err)
	}
	writeMigrationProgress(options.Progress, "preflight", "checking sandbox states")
	if err := validateStoppedLegacySandboxes(source); err != nil {
		return fail(err)
	}
	writeMigrationProgress(options.Progress, "database", "creating source snapshot")
	sourceSnapshot, err := openSourceDatabaseSnapshot(source)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = sourceSnapshot.Close() }()

	sourceDB := sourceSnapshot.db
	version, err := inspectVersion(ctx, sourceDB)
	if err != nil {
		return fail(err)
	}
	report.SourceVersion = version
	if version < 0 || version > currentSchemaVersion {
		return fail(fmt.Errorf("source database has an unknown migration version"))
	}
	if version == 0 {
		if err := validateUnversionedSource(ctx, sourceDB); err != nil {
			return fail(err)
		}
	} else if err := validateVersionedPrefix(ctx, sourceDB, version); err != nil {
		return fail(err)
	}
	if options.DryRun {
		warnings, targetVersion, copiedFiles, copiedBytes, err := dryRunMigration(ctx, sourceDB, source, runtimeRoot, inPlace, options.Progress)
		if err != nil {
			return fail(err)
		}
		report.Warnings = append(report.Warnings, warnings...)
		report.TargetVersion = targetVersion
		if inPlace {
			report.CheckedFiles = copiedFiles
			report.CheckedBytes = copiedBytes
		} else {
			report.CopiedFiles = copiedFiles
			report.CopiedBytes = copiedBytes
		}
		report.Stage = "eligible"
		writeMigrationProgress(options.Progress, "complete", "data root is eligible")
		return report, nil
	}
	if inPlace {
		return runInPlaceMigration(ctx, report, sourceDB, source, runtimeRoot, options.Progress)
	}
	fingerprint, err := fingerprintRootFromDatabase(source, sourceSnapshot.path)
	if err != nil {
		return fail(err)
	}
	report.SourceFingerprint = fingerprint
	state, err := prepareTarget(target, fingerprint, runtimeRoot)
	if err != nil {
		return fail(err)
	}
	if state.Complete {
		report.Warnings = append(report.Warnings, state.Warnings...)
		report.Stage = "complete"
		report.TargetVersion, err = verifyLatestTargetDatabase(ctx, filepath.Join(target, databaseName))
		if err != nil {
			return fail(err)
		}
		return report, nil
	}
	report.Warnings = append(report.Warnings, state.Warnings...)
	if state.Stage == "files" {
		report.Stage = "files"
		report.TargetVersion, err = verifyLatestTargetDatabase(ctx, filepath.Join(target, databaseName))
		if err != nil {
			return fail(err)
		}
		report.CopiedFiles, report.CopiedBytes, err = copyAuthoritativeFiles(source, target, runtimeRoot, state.SchedulerIDs, state.AgentIDs)
		if err != nil {
			return fail(err)
		}
		state.Stage = "complete"
		state.Complete = true
		if err := writeJournal(target, state); err != nil {
			return fail(err)
		}
		report.Stage = "complete"
		return report, nil
	}
	if state.Stage == "" {
		state.Stage = "database"
		if err := writeJournal(target, state); err != nil {
			return fail(err)
		}
	}
	report.Stage = "database"
	targetDBPath := filepath.Join(target, databaseName)
	if _, err := os.Stat(targetDBPath); errors.Is(err, os.ErrNotExist) {
		if err := snapshotDatabase(ctx, sourceDB, targetDBPath); err != nil {
			return fail(err)
		}
	} else if err != nil {
		return fail(fmt.Errorf("inspect target database: %w", err))
	}
	targetDB, err := sql.Open("sqlite", targetDBPath)
	if err != nil {
		return fail(fmt.Errorf("open target database: %w", err))
	}
	targetDB.SetMaxOpenConns(1)
	checkpoint := func(warnings []string, schedulerIDs map[string]string, agentIDs map[string]standaloneAgentIdentity) error {
		state.Warnings = append([]string(nil), warnings...)
		state.SchedulerIDs = cloneSchedulerIDs(schedulerIDs)
		state.AgentIDs = cloneStandaloneAgentIdentities(agentIDs)
		return writeJournal(target, state)
	}
	warnings, schedulerIDs, agentIDs, err := prepareTargetDatabase(ctx, targetDB, source, runtimeRoot, state.Warnings, state.SchedulerIDs, state.AgentIDs, checkpoint)
	if err != nil {
		_ = targetDB.Close()
		return fail(err)
	}
	report.Warnings = append([]string(nil), warnings...)
	report.TargetVersion, err = inspectVersion(ctx, targetDB)
	if closeErr := targetDB.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return fail(fmt.Errorf("verify target database: %w", err))
	}

	state.Stage = "files"
	state.Warnings = append([]string(nil), warnings...)
	state.SchedulerIDs = cloneSchedulerIDs(schedulerIDs)
	state.AgentIDs = cloneStandaloneAgentIdentities(agentIDs)
	if err := writeJournal(target, state); err != nil {
		return fail(err)
	}
	report.Stage = "files"
	report.CopiedFiles, report.CopiedBytes, err = copyAuthoritativeFiles(source, target, runtimeRoot, schedulerIDs, agentIDs)
	if err != nil {
		return fail(err)
	}
	state.Stage = "complete"
	state.Complete = true
	if err := writeJournal(target, state); err != nil {
		return fail(err)
	}
	report.Stage = "complete"
	return report, nil
}

func dryRunMigration(ctx context.Context, sourceDB *sql.DB, source, runtimeRoot string, inPlace bool, progress io.Writer) ([]string, int64, int, int64, error) {
	temporaryRoot, err := os.MkdirTemp("", "agent-compose-migrate-dry-run-*")
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("create dry-run workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporaryRoot) }()
	targetDBPath := filepath.Join(temporaryRoot, databaseName)
	if err := snapshotDatabase(ctx, sourceDB, targetDBPath); err != nil {
		return nil, 0, 0, 0, err
	}
	targetDB, err := sql.Open("sqlite", targetDBPath)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("open dry-run database: %w", err)
	}
	targetDB.SetMaxOpenConns(1)
	writeMigrationProgress(progress, "database", "simulating schema and data migration")
	warnings, schedulerIDs, agentIDs, err := prepareTargetDatabase(ctx, targetDB, source, runtimeRoot, nil, nil, nil, nil)
	if err != nil {
		_ = targetDB.Close()
		return nil, 0, 0, 0, err
	}
	targetVersion, err := inspectVersion(ctx, targetDB)
	closeErr := targetDB.Close()
	if err != nil {
		return nil, 0, 0, 0, err
	}
	if closeErr != nil {
		return nil, 0, 0, 0, fmt.Errorf("close dry-run database: %w", closeErr)
	}
	writeMigrationProgress(progress, "files", "inspecting migration layout")
	if inPlace {
		checkedFiles, err := inspectInPlaceAuthoritativeFiles(ctx, source, runtimeRoot, schedulerIDs, agentIDs, progress)
		return warnings, targetVersion, checkedFiles, 0, err
	}
	copiedFiles, copiedBytes, err := inspectAuthoritativeFiles(source, runtimeRoot, schedulerIDs, agentIDs)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	return warnings, targetVersion, copiedFiles, copiedBytes, nil
}

func writeMigrationProgress(writer io.Writer, stage, message string) {
	if writer == nil {
		return
	}
	_, _ = fmt.Fprintf(writer, "[%s] %s\n", stage, message)
}

func validateSourceRoot(source string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("inspect source: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source must be a data-root directory")
	}
	if _, err := os.Stat(filepath.Join(source, databaseName)); err != nil {
		return fmt.Errorf("inspect source database: %w", err)
	}
	return nil
}

func openReadOnly(path string) (*sql.DB, error) {
	u := &url.URL{Scheme: "file", Path: path}
	query := u.Query()
	query.Set("mode", "ro")
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open source database read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping source database: %w", err)
	}
	return db, nil
}

func inspectVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&exists); err != nil {
		return 0, fmt.Errorf("inspect source migration history: %w", err)
	}
	if exists == 0 {
		return 0, nil
	}
	var version int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read source migration version: %w", err)
	}
	return version, nil
}

func validateVersionedPrefix(ctx context.Context, db *sql.DB, version int64) error {
	rows, err := db.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("read source migration prefix: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var expected int64 = 1
	for rows.Next() {
		var current int64
		var checksum string
		if err := rows.Scan(&current, &checksum); err != nil {
			return fmt.Errorf("scan source migration prefix: %w", err)
		}
		if current != expected || knownMigrationChecksums[current] != checksum {
			return fmt.Errorf("source migration prefix is unknown at version %d", current)
		}
		expected++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if expected-1 != version {
		return fmt.Errorf("source migration history is not an exact prefix")
	}
	return nil
}

func snapshotDatabase(ctx context.Context, source *sql.DB, target string) error {
	if _, err := source.ExecContext(ctx, `VACUUM INTO ?`, target); err != nil {
		return fmt.Errorf("snapshot source database: %w", err)
	}
	return nil
}

func fingerprintRoot(root string) (string, error) {
	snapshot, err := openSourceDatabaseSnapshot(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = snapshot.Close() }()
	return fingerprintRootFromDatabase(root, snapshot.path)
}

func fingerprintRootFromDatabase(root, databasePath string) (string, error) {
	var entries []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		if rel == inPlaceBackupName && entry.IsDir() {
			return filepath.SkipDir
		}
		if rel == databaseName || rel == databaseName+"-wal" || rel == databaseName+"-shm" || rel == journalName {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		contentHash := ""
		if entry.Type()&os.ModeSymlink != 0 {
			if err := validateMigratableSourceSymlink(rel); err != nil {
				return err
			}
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			targetHash := sha256.Sum256([]byte(target))
			contentHash = "symlink:" + hex.EncodeToString(targetHash[:])
		} else if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			hash := sha256.New()
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			contentHash = hex.EncodeToString(hash.Sum(nil))
		}
		entries = append(entries, fmt.Sprintf("%s\x00%d\x00%d\x00%s", filepath.ToSlash(rel), info.Mode(), info.Size(), contentHash))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("fingerprint source: %w", err)
	}
	databaseHash, err := fingerprintDatabase(databasePath)
	if err != nil {
		return "", fmt.Errorf("fingerprint source database: %w", err)
	}
	entries = append(entries, databaseName+"\x00sqlite-snapshot\x00"+databaseHash)
	sort.Strings(entries)
	hash := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

func fingerprintDatabase(path string) (string, error) {
	db, err := openReadOnly(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()

	temporary, err := os.CreateTemp("", "agent-compose-source-fingerprint-*.db")
	if err != nil {
		return "", fmt.Errorf("create database fingerprint path: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return "", fmt.Errorf("close database fingerprint path: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return "", fmt.Errorf("prepare database fingerprint path: %w", err)
	}
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := snapshotDatabase(context.Background(), db, temporaryPath); err != nil {
		return "", err
	}
	file, err := os.Open(temporaryPath)
	if err != nil {
		return "", fmt.Errorf("open database fingerprint snapshot: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", fmt.Errorf("hash database fingerprint snapshot: %w", copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close database fingerprint snapshot: %w", closeErr)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
	databaseName = "data.db"
	journalName  = ".agent-compose-migrate.json"
)

var knownMigrationChecksums = map[int64]string{
	1: "6d2a07e2df01c38a57989accc3eb265cc3238ae3322f5dd540383235e59e27a9",
	2: "fa328b0bd1be3620d4a92b94bd39d6cb4a3a6d454ce1de643e82598f9c028a49",
	3: "5bdbd3258245ce7fc025121625408b35b444a425ffcb89aed0b8b7a846969183",
	4: "3d5c2ab028a6f7e1c461f0af3fc6d898807b60159f1625b8939987d6ce7b91cb",
	5: "dd9ded53e4c76b70f6da46cfd7f5cec59bb1dedf58460ab974fe72d41fe6a028",
	6: "163a5651a54e7b807f2d626d05818a345c7fe92fa4d6efecf04ac394b32e5d94",
	7: "3fa7341d89f6157a8d5ac700c92893a392060bd7303b46cfdccf8c78be05d0da",
}

var ErrReported = errors.New("migration failure is included in the report")

type Options struct {
	Source string
	Target string
	DryRun bool
	JSON   bool
}

type Report struct {
	Source            string   `json:"source"`
	Target            string   `json:"target"`
	SourceFingerprint string   `json:"source_fingerprint,omitempty"`
	SourceVersion     int64    `json:"source_version,omitempty"`
	TargetVersion     int64    `json:"target_version,omitempty"`
	Stage             string   `json:"stage"`
	CopiedFiles       int      `json:"copied_files,omitempty"`
	CopiedBytes       int64    `json:"copied_bytes,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
	Error             string   `json:"error,omitempty"`
	DryRun            bool     `json:"dry_run,omitempty"`
}

func (r Report) Text() string {
	if r.Error != "" {
		return fmt.Sprintf("legacy migration %s: %s", r.Stage, r.Error)
	}
	if r.DryRun {
		return fmt.Sprintf("legacy migration dry run: source schema version %d is eligible", r.SourceVersion)
	}
	return fmt.Sprintf("legacy migration complete: schema v%d, %d files (%d bytes) copied to %s", r.TargetVersion, r.CopiedFiles, r.CopiedBytes, r.Target)
}

type journal struct {
	SourceFingerprint string `json:"source_fingerprint"`
	Stage             string `json:"stage"`
	Complete          bool   `json:"complete"`
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
	if source == target {
		return fail(fmt.Errorf("source and target must be different directories"))
	}
	if err := validateSourceRoot(source); err != nil {
		return fail(err)
	}
	fingerprint, err := fingerprintRoot(source)
	if err != nil {
		return fail(err)
	}
	report.SourceFingerprint = fingerprint

	sourceDB, err := openReadOnly(filepath.Join(source, databaseName))
	if err != nil {
		return fail(err)
	}
	defer func() { _ = sourceDB.Close() }()
	version, err := inspectVersion(ctx, sourceDB)
	if err != nil {
		return fail(err)
	}
	report.SourceVersion = version
	if version < 0 || version > 7 {
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
		warnings, targetVersion, copiedFiles, copiedBytes, err := dryRunMigration(ctx, sourceDB, source, version)
		if err != nil {
			return fail(err)
		}
		report.Warnings = append(report.Warnings, warnings...)
		report.TargetVersion = targetVersion
		report.CopiedFiles = copiedFiles
		report.CopiedBytes = copiedBytes
		report.Stage = "eligible"
		return report, nil
	}

	state, err := prepareTarget(target, fingerprint)
	if err != nil {
		return fail(err)
	}
	if state.Complete {
		report.Stage = "complete"
		report.TargetVersion = 7
		return report, nil
	}
	if err := writeJournal(target, journal{SourceFingerprint: fingerprint, Stage: "database"}); err != nil {
		return fail(err)
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
	warnings, schedulerIDs, err := prepareTargetDatabase(ctx, targetDB, version, source, target)
	if err != nil {
		_ = targetDB.Close()
		return fail(err)
	}
	report.Warnings = append(report.Warnings, warnings...)
	report.TargetVersion, err = inspectVersion(ctx, targetDB)
	if closeErr := targetDB.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return fail(fmt.Errorf("verify target database: %w", err))
	}

	if err := writeJournal(target, journal{SourceFingerprint: fingerprint, Stage: "files"}); err != nil {
		return fail(err)
	}
	report.Stage = "files"
	report.CopiedFiles, report.CopiedBytes, err = copyAuthoritativeFiles(source, target, schedulerIDs)
	if err != nil {
		return fail(err)
	}
	if err := writeJournal(target, journal{SourceFingerprint: fingerprint, Stage: "complete", Complete: true}); err != nil {
		return fail(err)
	}
	report.Stage = "complete"
	return report, nil
}

func dryRunMigration(ctx context.Context, sourceDB *sql.DB, source string, version int64) ([]string, int64, int, int64, error) {
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
	warnings, schedulerIDs, err := prepareTargetDatabase(ctx, targetDB, version, source, temporaryRoot)
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
	copiedFiles, copiedBytes, err := copyAuthoritativeFiles(source, temporaryRoot, schedulerIDs)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	return warnings, targetVersion, copiedFiles, copiedBytes, nil
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

func prepareTarget(target, fingerprint string) (journal, error) {
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(target, 0o700); err != nil {
			return journal{}, fmt.Errorf("create target: %w", err)
		}
		return journal{SourceFingerprint: fingerprint}, nil
	}
	if err != nil {
		return journal{}, fmt.Errorf("inspect target: %w", err)
	}
	if !info.IsDir() {
		return journal{}, fmt.Errorf("target must be a directory")
	}
	data, err := os.ReadFile(filepath.Join(target, journalName))
	if err != nil {
		return journal{}, fmt.Errorf("target exists without a resumable migration journal")
	}
	var state journal
	if err := json.Unmarshal(data, &state); err != nil {
		return journal{}, fmt.Errorf("decode target migration journal: %w", err)
	}
	if state.SourceFingerprint != fingerprint {
		return journal{}, fmt.Errorf("source fingerprint changed since the target migration started")
	}
	return state, nil
}

func snapshotDatabase(ctx context.Context, source *sql.DB, target string) error {
	if _, err := source.ExecContext(ctx, `VACUUM INTO ?`, target); err != nil {
		return fmt.Errorf("snapshot source database: %w", err)
	}
	return nil
}

func fingerprintRoot(root string) (string, error) {
	var entries []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse symlink in source data root: %s", rel)
		}
		if rel == databaseName || rel == databaseName+"-wal" || rel == databaseName+"-shm" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		contentHash := ""
		if info.Mode().IsRegular() {
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
	databaseHash, err := fingerprintDatabase(filepath.Join(root, databaseName))
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

func writeJournal(target string, state journal) error {
	stateData, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temporary := filepath.Join(target, journalName+".tmp")
	if err := os.WriteFile(temporary, append(stateData, '\n'), 0o600); err != nil {
		return fmt.Errorf("write migration journal: %w", err)
	}
	if err := os.Rename(temporary, filepath.Join(target, journalName)); err != nil {
		return fmt.Errorf("seal migration journal: %w", err)
	}
	return nil
}

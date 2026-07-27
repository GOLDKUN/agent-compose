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

	"agent-compose/pkg/storage/sqlite"

	_ "modernc.org/sqlite"
)

const (
	databaseName = "data.db"
	journalName  = ".agent-compose-legacy-migrate.json"
)

var knownMigrationChecksums = map[int64]string{
	1: "6d2a07e2df01c38a57989accc3eb265cc3238ae3322f5dd540383235e59e27a9",
	2: "fa328b0bd1be3620d4a92b94bd39d6cb4a3a6d454ce1de643e82598f9c028a49",
	3: "5bdbd3258245ce7fc025121625408b35b444a425ffcb89aed0b8b7a846969183",
	4: "3d5c2ab028a6f7e1c461f0af3fc6d898807b60159f1625b8939987d6ce7b91cb",
	5: "d8822e3a8a6a4b4f6571f7b42989d46d5fe46d585ae2290567f651dcfb1fda3f",
	6: "ee52d06fe337d5f19216f69d8d17fcdf20f8f742baf7c2d77173a9060cfd27c9",
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
	if version < 1 || version > 7 {
		return fail(fmt.Errorf("source database is unversioned or has an unknown migration prefix; deterministic legacy shape conversion is required"))
	}
	if err := validateVersionedPrefix(ctx, sourceDB, version); err != nil {
		return fail(err)
	}
	if err := rejectStandaloneV1Rows(ctx, sourceDB, version); err != nil {
		return fail(err)
	}
	if options.DryRun {
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
	if err := sqlite.Migrate(ctx, targetDB); err != nil {
		_ = targetDB.Close()
		return fail(fmt.Errorf("migrate target database: %w", err))
	}
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
	report.CopiedFiles, report.CopiedBytes, err = copyAuthoritativeFiles(source, target)
	if err != nil {
		return fail(err)
	}
	if err := writeJournal(target, journal{SourceFingerprint: fingerprint, Stage: "complete", Complete: true}); err != nil {
		return fail(err)
	}
	report.Stage = "complete"
	return report, nil
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

func rejectStandaloneV1Rows(ctx context.Context, db *sql.DB, version int64) error {
	if version >= 5 {
		return nil
	}
	var count int
	query := `SELECT
		(SELECT COUNT(*) FROM agent_definition d LEFT JOIN project_agent a ON a.managed_agent_id=d.id WHERE a.id IS NULL) +
		(SELECT COUNT(*) FROM loader l LEFT JOIN project_scheduler s ON s.managed_loader_id=l.id WHERE s.id IS NULL)`
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return fmt.Errorf("inspect standalone legacy definitions: %w", err)
	}
	if count != 0 {
		return fmt.Errorf("source contains %d standalone agent or loader definitions; refusing to guess project ownership", count)
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

func copyAuthoritativeFiles(source, target string) (int, int64, error) {
	var files int
	var bytes int64
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse symlink in source data root: %s", rel)
		}
		if rel == databaseName || rel == databaseName+"-wal" || rel == databaseName+"-shm" || rel == journalName {
			return nil
		}
		destination := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refuse non-regular source file: %s", rel)
		}
		if err := copyFile(path, destination, info.Mode().Perm()); err != nil {
			return err
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
}

func copyFile(source, target string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
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
		if rel == databaseName+"-wal" || rel == databaseName+"-shm" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		entries = append(entries, fmt.Sprintf("%s\x00%d\x00%d\x00%d", filepath.ToSlash(rel), info.Mode(), info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("fingerprint source: %w", err)
	}
	sort.Strings(entries)
	hash := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return "sha256:" + hex.EncodeToString(hash[:]), nil
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

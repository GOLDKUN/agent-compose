package migrate

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type sourceDatabaseSnapshot struct {
	db   *sql.DB
	path string
	root string
}

func openSourceDatabaseSnapshot(sourceRoot string) (*sourceDatabaseSnapshot, error) {
	temporaryRoot, err := os.MkdirTemp("", "agent-compose-source-snapshot-*")
	if err != nil {
		return nil, fmt.Errorf("create source database snapshot directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(temporaryRoot) }
	for _, name := range []string{databaseName, databaseName + "-wal"} {
		sourcePath := filepath.Join(sourceRoot, name)
		info, err := os.Lstat(sourcePath)
		if errors.Is(err, os.ErrNotExist) && name != databaseName {
			continue
		}
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("inspect source database file %s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			cleanup()
			return nil, fmt.Errorf("source database file %s must be regular", name)
		}
		if err := copyFile(sourcePath, filepath.Join(temporaryRoot, name), 0o600); err != nil {
			cleanup()
			return nil, fmt.Errorf("copy source database file %s: %w", name, err)
		}
	}
	snapshotPath := filepath.Join(temporaryRoot, databaseName)
	db, err := sql.Open("sqlite", snapshotPath)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("open source database snapshot: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		cleanup()
		return nil, fmt.Errorf("read source database snapshot: %w", err)
	}
	return &sourceDatabaseSnapshot{db: db, path: snapshotPath, root: temporaryRoot}, nil
}

func (s *sourceDatabaseSnapshot) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	if s.db != nil {
		closeErr = s.db.Close()
		s.db = nil
	}
	removeErr := os.RemoveAll(s.root)
	return errors.Join(closeErr, removeErr)
}

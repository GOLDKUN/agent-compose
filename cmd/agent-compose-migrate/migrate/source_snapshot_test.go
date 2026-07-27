package migrate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"agent-compose/pkg/storage/sqlite"
)

func TestRunDoesNotOpenSourceSQLiteFiles(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(filepath.Join(source, databaseName), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.DB().Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().Exec(`INSERT INTO project(id,name,current_revision,spec_hash,created_at,updated_at) VALUES('source-lock-test','source-lock-test',0,'',1,1)`); err != nil {
		t.Fatal(err)
	}
	shmPath := filepath.Join(source, databaseName+"-shm")
	before, err := os.ReadFile(shmPath)
	if err != nil {
		t.Fatalf("read source SHM before migration: %v", err)
	}

	report, err := Run(context.Background(), Options{
		Source: source, Target: filepath.Join(t.TempDir(), "target"), DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry-run report=%+v err=%v", report, err)
	}
	after, err := os.ReadFile(shmPath)
	if err != nil {
		t.Fatalf("read source SHM after migration: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("migration changed source SQLite shared-memory bytes")
	}
}

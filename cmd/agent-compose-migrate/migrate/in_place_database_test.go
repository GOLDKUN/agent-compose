package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agent-compose/pkg/storage/sqlite"
)

func TestSwitchInPlaceDatabaseActivatesCommittedWALFrames(t *testing.T) {
	root := t.TempDir()
	backupRoot := filepath.Join(root, inPlaceBackupName)
	if err := os.Mkdir(backupRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	stagingPath := filepath.Join(t.TempDir(), "staging.db")
	staging, err := sqlite.Open(stagingPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = staging.Close() })
	if _, err := staging.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := staging.DB().Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := staging.DB().Exec(`INSERT INTO project(id,name,current_revision,spec_hash,created_at,updated_at) VALUES('wal-project','wal-project',0,'',1,1)`); err != nil {
		t.Fatal(err)
	}

	convertedPath := filepath.Join(backupRoot, inPlaceConvertedDB)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		sourcePath := stagingPath + suffix
		info, err := os.Stat(sourcePath)
		if err != nil {
			t.Fatalf("stat staging database file %s: %v", filepath.Base(sourcePath), err)
		}
		if err := copyFile(sourcePath, convertedPath+suffix, info.Mode().Perm()); err != nil {
			t.Fatalf("copy staging database file %s: %v", filepath.Base(sourcePath), err)
		}
	}

	if err := switchInPlaceDatabase(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	target, err := openReadOnly(filepath.Join(root, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()
	var projects int
	if err := target.QueryRow(`SELECT COUNT(*) FROM project WHERE id='wal-project'`).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if projects != 1 {
		t.Fatalf("activated database contains %d WAL-backed projects, want 1", projects)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(convertedPath + suffix); !os.IsNotExist(err) {
			t.Fatalf("converted database companion %s remains after activation: %v", suffix, err)
		}
	}
}

package sqlite

import (
	"context"
	"strings"
	"testing"
)

func TestIntegrationSQLitePublicMigrationAPIWorkflow(t *testing.T) {
	testSQLitePublicMigrationAPIWorkflow(t)
}

func TestE2ESQLitePublicMigrationAPIWorkflow(t *testing.T) {
	testSQLitePublicMigrationAPIWorkflow(t)
}

func TestIntegrationSQLiteProjectNameMigrationWorkflow(t *testing.T) {
	testUniqueProjectNameMigrationRenamesDuplicatesInStableOrder(t)
}

func TestE2ESQLiteProjectNameMigrationWorkflow(t *testing.T) {
	testUniqueProjectNameMigrationRenamesDuplicatesInStableOrder(t)
}

func testSQLitePublicMigrationAPIWorkflow(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	if err := Migrate(ctx, nil); err == nil || !strings.Contains(err.Error(), "SQLite database is required") {
		t.Fatalf("Migrate(nil) error = %v, want required-database error", err)
	}
	if err := MigrateThrough(ctx, nil, 1); err == nil || !strings.Contains(err.Error(), "SQLite database is required") {
		t.Fatalf("MigrateThrough(nil) error = %v, want required-database error", err)
	}

	migration, found, err := EmbeddedMigrationAt(1)
	if err != nil {
		t.Fatalf("EmbeddedMigrationAt(1): %v", err)
	}
	if !found || migration.Version != 1 || migration.Name == "" || migration.Statement == "" || migration.Checksum == "" {
		t.Fatalf("EmbeddedMigrationAt(1) = (%+v, %v), want a complete migration", migration, found)
	}
	if _, found, err := EmbeddedMigrationAt(-1); err != nil {
		t.Fatalf("EmbeddedMigrationAt(-1): %v", err)
	} else if found {
		t.Fatal("EmbeddedMigrationAt(-1) found an unexpected migration")
	}

	db := newMemoryDB(t)
	if err := MigrateThrough(ctx, db, 0); err == nil || !strings.Contains(err.Error(), "no embedded SQLite migrations") {
		t.Fatalf("MigrateThrough(0) error = %v, want no-migrations error", err)
	}
	if err := MigrateThrough(ctx, db, 1); err != nil {
		t.Fatalf("MigrateThrough(1): %v", err)
	}
	var applied int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count migrations after MigrateThrough: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied migration count = %d, want 1", applied)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}

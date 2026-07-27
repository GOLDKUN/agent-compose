package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenUsesDefaultRuntimeConnectionLimitAfterMigration(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "data.db"), 0)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if got := database.DB().Stats().MaxOpenConnections; got != DefaultMaxOpenConns {
		t.Fatalf("MaxOpenConnections = %d, want %d", got, DefaultMaxOpenConns)
	}
	if err := database.DB().QueryRowContext(context.Background(), `SELECT count(*) FROM schema_migrations`).Scan(new(int)); err != nil {
		t.Fatalf("query migrated schema: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	connections := make([]*sql.Conn, 0, DefaultMaxOpenConns)
	for range DefaultMaxOpenConns {
		connection, err := database.DB().Conn(ctx)
		if err != nil {
			t.Fatalf("acquire runtime connection: %v", err)
		}
		connections = append(connections, connection)
	}
	for _, connection := range connections {
		if err := connection.Close(); err != nil {
			t.Errorf("release runtime connection: %v", err)
		}
	}
}

func TestOpenWithMaxOpenConnsUsesConfiguredLimit(t *testing.T) {
	database, err := OpenWithMaxOpenConns(filepath.Join(t.TempDir(), "configured.db"), 0, 7)
	if err != nil {
		t.Fatalf("OpenWithMaxOpenConns returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if got := database.DB().Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("MaxOpenConnections = %d, want 7", got)
	}
}

func TestOpenKeepsMemoryDatabaseOnOneConnection(t *testing.T) {
	for _, path := range []string{
		":memory:",
		"file::memory:?cache=shared",
		"file:test-memory?mode=memory&cache=shared",
	} {
		t.Run(path, func(t *testing.T) {
			database, err := Open(path, 0)
			if err != nil {
				t.Fatalf("Open returned error: %v", err)
			}
			t.Cleanup(func() {
				if err := database.Close(); err != nil {
					t.Errorf("close database: %v", err)
				}
			})

			if got := database.DB().Stats().MaxOpenConnections; got != 1 {
				t.Fatalf("MaxOpenConnections = %d, want 1", got)
			}
			if err := database.DB().QueryRowContext(context.Background(), `SELECT count(*) FROM schema_migrations`).Scan(new(int)); err != nil {
				t.Fatalf("query migrated schema: %v", err)
			}
		})
	}
}

func TestOpenRejectsNonPositiveRuntimeConnectionLimit(t *testing.T) {
	if _, err := OpenWithMaxOpenConns(filepath.Join(t.TempDir(), "data.db"), 0, 0); err == nil {
		t.Fatal("OpenWithMaxOpenConns returned nil error")
	}
}

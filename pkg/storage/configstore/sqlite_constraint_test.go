package configstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestIsSQLiteDuplicateRowMatchesConstraintKinds pins which constraint failures
// count as a duplicate. It exists because the classification used to read the
// error message, and a code-based check is only equivalent if it covers both the
// unique and primary-key violations that message matched.
func TestIsSQLiteDuplicateRowMatchesConstraintKinds(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "constraint.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys(1)`); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE parent(id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE child(
		id TEXT PRIMARY KEY,
		name TEXT UNIQUE,
		required TEXT NOT NULL,
		parent_id TEXT REFERENCES parent(id))`); err != nil {
		t.Fatalf("create child: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO parent VALUES('p1')`); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO child VALUES('c1', 'taken', 'value', 'p1')`); err != nil {
		t.Fatalf("seed child: %v", err)
	}

	for _, test := range []struct {
		name  string
		query string
		args  []any
		want  bool
	}{
		{
			name:  "unique violation is a duplicate",
			query: `INSERT INTO child VALUES('c2', 'taken', 'value', 'p1')`,
			want:  true,
		},
		{
			name:  "primary key violation is a duplicate",
			query: `INSERT INTO child VALUES('c1', 'fresh', 'value', 'p1')`,
			want:  true,
		},
		{
			name:  "foreign key violation is not a duplicate",
			query: `INSERT INTO child VALUES('c3', 'fresh', 'value', 'missing')`,
			want:  false,
		},
		{
			name:  "not null violation is not a duplicate",
			query: `INSERT INTO child(id, name, parent_id) VALUES('c4', 'fresh', 'p1')`,
			want:  false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := db.ExecContext(ctx, test.query, test.args...)
			if err == nil {
				t.Fatal("expected the insert to fail")
			}
			if got := isSQLiteDuplicateRow(err); got != test.want {
				t.Fatalf("isSQLiteDuplicateRow(%v) = %v, want %v", err, got, test.want)
			}
		})
	}

	t.Run("unrelated errors are not duplicates", func(t *testing.T) {
		if isSQLiteDuplicateRow(nil) {
			t.Fatal("nil must not be a duplicate")
		}
		if isSQLiteDuplicateRow(errors.New("UNIQUE constraint failed: child.name")) {
			t.Fatal("a plain error carrying the old message must not be a duplicate")
		}
	})
}

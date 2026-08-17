package configstore

import (
	"errors"

	sqlitedrv "modernc.org/sqlite"
)

// SQLite reports constraint violations through extended result codes. Only the
// two below mean "a row with this key already exists"; the other SQLITE_CONSTRAINT
// codes (foreign key, not null, check) describe different failures and must not
// be reported as a duplicate.
//
// The values are part of SQLite's stable public interface. They are spelled out
// here rather than imported from modernc.org/sqlite/lib, which is a very large
// generated package pulled in per GOARCH.
const (
	sqliteConstraintUnique     = 2067 // SQLITE_CONSTRAINT_UNIQUE
	sqliteConstraintPrimaryKey = 1555 // SQLITE_CONSTRAINT_PRIMARYKEY
)

// isSQLiteDuplicateRow reports whether err is the driver's way of saying the
// insert collided with an existing row.
func isSQLiteDuplicateRow(err error) bool {
	var sqliteErr *sqlitedrv.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code() {
	case sqliteConstraintUnique, sqliteConstraintPrimaryKey:
		return true
	default:
		return false
	}
}

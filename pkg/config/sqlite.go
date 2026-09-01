package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	storagesqlite "github.com/chaitin/agent-compose/pkg/storage/sqlite"
)

const (
	DefaultSQLiteMaxOpenConns = storagesqlite.DefaultMaxOpenConns
	maxSQLiteMaxOpenConns     = 32
)

func sqliteMaxOpenConnsFromEnvironment() (int, error) {
	raw := strings.TrimSpace(os.Getenv("SQLITE_MAX_OPEN_CONNS"))
	if raw == "" {
		return DefaultSQLiteMaxOpenConns, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maxSQLiteMaxOpenConns {
		return 0, fmt.Errorf("SQLITE_MAX_OPEN_CONNS must be an integer between 1 and %d", maxSQLiteMaxOpenConns)
	}
	return value, nil
}

// EffectiveSQLiteMaxOpenConns returns the configured runtime connection
// limit. The fallback keeps manually constructed Config values aligned with
// the daemon default.
func (c *Config) EffectiveSQLiteMaxOpenConns() int {
	if c == nil || c.SQLiteMaxOpenConns == 0 {
		return DefaultSQLiteMaxOpenConns
	}
	return c.SQLiteMaxOpenConns
}

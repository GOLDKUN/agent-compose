package config

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/samber/do/v2"
)

func TestNewConfigSQLiteMaxOpenConns(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{name: "default", want: DefaultSQLiteMaxOpenConns},
		{name: "configured", value: "8", want: 8},
		{name: "minimum", value: "1", want: 1},
		{name: "maximum", value: "32", want: 32},
		{name: "zero", value: "0", wantErr: true},
		{name: "above maximum", value: "33", wantErr: true},
		{name: "not an integer", value: "many", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DATA_ROOT", filepath.Join(t.TempDir(), "data"))
			t.Setenv("SQLITE_MAX_OPEN_CONNS", test.value)
			di := do.New()
			do.ProvideValue(di, slog.Default())

			config, err := NewConfig(di)
			if test.wantErr {
				if err == nil {
					t.Fatal("NewConfig returned nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewConfig returned error: %v", err)
			}
			if config.SQLiteMaxOpenConns != test.want {
				t.Fatalf("SQLiteMaxOpenConns = %d, want %d", config.SQLiteMaxOpenConns, test.want)
			}
		})
	}
}

func TestConfigEffectiveSQLiteMaxOpenConnsDefaultsConstructedValues(t *testing.T) {
	if got := (&Config{}).EffectiveSQLiteMaxOpenConns(); got != DefaultSQLiteMaxOpenConns {
		t.Fatalf("EffectiveSQLiteMaxOpenConns = %d, want %d", got, DefaultSQLiteMaxOpenConns)
	}
	if got := (&Config{SQLiteMaxOpenConns: 7}).EffectiveSQLiteMaxOpenConns(); got != 7 {
		t.Fatalf("EffectiveSQLiteMaxOpenConns = %d, want 7", got)
	}
}

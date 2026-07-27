package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-compose/cmd/agent-compose-migrate/migrate"
	"agent-compose/pkg/storage/sqlite"
)

func TestE2ELegacyMigratorCLIJSONDryRun(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(filepath.Join(source, "data.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = originalStdout })
	exitCode := run(context.Background(), []string{"--source", source, "--target", target, "--dry-run", "--json"})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = originalStdout
	var report migrate.Report
	if err := json.NewDecoder(reader).Decode(&report); err != nil {
		t.Fatalf("decode CLI report: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if exitCode != 0 || report.Stage != "eligible" || report.TargetVersion != 7 || !report.DryRun {
		t.Fatalf("exit=%d report=%+v", exitCode, report)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("dry-run created target: %v", err)
	}
}

func TestE2ELegacyMigratorCLITextDryRun(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(filepath.Join(source, "data.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = originalStdout })
	exitCode := run(context.Background(), []string{"--source", source, "--target", target, "--dry-run"})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = originalStdout
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if got, want := strings.TrimSpace(string(output)), "legacy migration dry run: source schema version 7 is eligible"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("dry-run created target: %v", err)
	}
}

func TestE2ELegacyMigratorCLIExitContracts(t *testing.T) {
	if exitCode := run(context.Background(), []string{"unexpected"}); exitCode != 2 {
		t.Fatalf("positional argument exit code = %d, want 2", exitCode)
	}
	if exitCode := run(context.Background(), []string{"--unknown"}); exitCode != 2 {
		t.Fatalf("unknown flag exit code = %d, want 2", exitCode)
	}
	if exitCode := run(context.Background(), nil); exitCode != 1 {
		t.Fatalf("missing required flags exit code = %d, want 1", exitCode)
	}
}

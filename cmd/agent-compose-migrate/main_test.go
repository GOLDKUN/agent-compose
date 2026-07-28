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
	if exitCode != 0 || report.Stage != "eligible" || report.TargetVersion != 8 || !report.DryRun {
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
	if got, want := strings.TrimSpace(string(output)), "legacy migration dry run: source schema version 9 is eligible"; got != want {
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

func TestE2ELegacyMigratorCLIInPlaceWorkflow(t *testing.T) {
	root := t.TempDir()
	db, err := sqlite.Open(filepath.Join(root, "data.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	sandboxID := "sandbox-1"
	legacySandbox := filepath.Join(root, "sessions", sandboxID)
	if err := os.MkdirAll(legacySandbox, 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(legacySandbox, "state.bin")
	if err := os.WriteFile(statePath, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyInfo, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]any{"summary": map[string]any{
		"id": sandboxID, "vm_status": "STOPPED", "workspace_path": filepath.Join(root, "sessions", sandboxID, "workspace"),
	}}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacySandbox, "metadata.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	dryRun := runMigratorCLIJSON(t, "--source", root, "--target", root, "--dry-run", "--json")
	if dryRun.Stage != "eligible" || !dryRun.InPlace || dryRun.CheckedFiles != 1 || dryRun.CopiedFiles != 0 {
		t.Fatalf("in-place dry-run report=%+v", dryRun)
	}
	if _, err := os.Stat(filepath.Join(root, "sessions", sandboxID, "state.bin")); err != nil {
		t.Fatalf("dry run changed sandbox state: %v", err)
	}

	report := runMigratorCLIJSON(t, "--source", root, "--target", root, "--json")
	if report.Stage != "complete" || !report.InPlace || report.TargetVersion != 8 || report.Backup == "" {
		t.Fatalf("in-place report=%+v", report)
	}
	nativeInfo, err := os.Stat(filepath.Join(root, "sandboxes", sandboxID, "state.bin"))
	if err != nil || !os.SameFile(legacyInfo, nativeInfo) {
		t.Fatalf("sandbox state was not renamed in place: info=%v err=%v", nativeInfo, err)
	}
	if _, err := os.Stat(filepath.Join(report.Backup, "data.v1.db")); err != nil {
		t.Fatalf("database backup is unavailable: %v", err)
	}
	repeated := runMigratorCLIJSON(t, "--source", root, "--target", root, "--json")
	if repeated.Stage != "complete" || repeated.TargetVersion != 8 {
		t.Fatalf("repeated in-place report=%+v", repeated)
	}
}

func runMigratorCLIJSON(t *testing.T, args ...string) migrate.Report {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	os.Stdout = writer
	exitCode := run(context.Background(), args)
	if err := writer.Close(); err != nil {
		os.Stdout = originalStdout
		t.Fatal(err)
	}
	os.Stdout = originalStdout
	var report migrate.Report
	if err := json.NewDecoder(reader).Decode(&report); err != nil {
		_ = reader.Close()
		t.Fatalf("decode CLI report: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if exitCode != 0 {
		t.Fatalf("CLI exit=%d report=%+v", exitCode, report)
	}
	return report
}

//go:build k8scompose

package driver

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func buildTestTar(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for name, content := range entries {
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatalf("write tar header for %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write tar content for %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	return buf.Bytes()
}

func TestExtractTarArchiveWritesFilesAndSubdirs(t *testing.T) {
	dest := t.TempDir()
	data := buildTestTar(t, map[string]string{
		"exitcode.txt":     "0",
		"nested/output.md": "# result",
	})

	if err := extractTarArchive(bytes.NewReader(data), dest); err != nil {
		t.Fatalf("extractTarArchive() error = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "exitcode.txt"))
	if err != nil {
		t.Fatalf("read extracted exitcode.txt: %v", err)
	}
	if string(got) != "0" {
		t.Fatalf("exitcode.txt content = %q, want %q", got, "0")
	}

	got, err = os.ReadFile(filepath.Join(dest, "nested", "output.md"))
	if err != nil {
		t.Fatalf("read extracted nested/output.md: %v", err)
	}
	if string(got) != "# result" {
		t.Fatalf("nested/output.md content = %q, want %q", got, "# result")
	}
}

func TestBuildTarArchiveRoundTripsThroughExtractTarArchive(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# skill"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "data.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatalf("write nested/data.json: %v", err)
	}

	archive, err := buildTarArchive(src)
	if err != nil {
		t.Fatalf("buildTarArchive() error = %v", err)
	}

	dest := t.TempDir()
	if err := extractTarArchive(bytes.NewReader(archive), dest); err != nil {
		t.Fatalf("extractTarArchive() error = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if err != nil {
		t.Fatalf("read extracted SKILL.md: %v", err)
	}
	if string(got) != "# skill" {
		t.Fatalf("SKILL.md content = %q, want %q", got, "# skill")
	}
	got, err = os.ReadFile(filepath.Join(dest, "nested", "data.json"))
	if err != nil {
		t.Fatalf("read extracted nested/data.json: %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("nested/data.json content = %q, want %q", got, `{"ok":true}`)
	}
}

func TestWriteTarArchiveSupportsWorkspaceLargerThanLegacyArgLimit(t *testing.T) {
	src := t.TempDir()
	content := bytes.Repeat([]byte("workspace-data\n"), 64*1024)
	if len(content) <= 512*1024 {
		t.Fatalf("test payload = %d bytes, want larger than legacy 512 KiB limit", len(content))
	}
	if err := os.WriteFile(filepath.Join(src, "large.txt"), content, 0o644); err != nil {
		t.Fatalf("write large workspace file: %v", err)
	}

	var archive bytes.Buffer
	if err := writeTarArchive(&archive, src); err != nil {
		t.Fatalf("writeTarArchive() error = %v", err)
	}
	dest := t.TempDir()
	if err := extractTarArchive(bytes.NewReader(archive.Bytes()), dest); err != nil {
		t.Fatalf("extractTarArchive() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "large.txt"))
	if err != nil {
		t.Fatalf("read extracted large workspace file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("extracted workspace content length = %d, want %d", len(got), len(content))
	}
}

func TestWriteGuestDirRefusesGuestRootReplacement(t *testing.T) {
	runtime := &k8sRuntime{}
	err := runtime.WriteGuestDir(context.Background(), nil, VMState{}, t.TempDir(), "/")
	if err == nil || err.Error() != "write guest dir: refusing to replace guest root" {
		t.Fatalf("WriteGuestDir root error = %v", err)
	}
}

func TestExtractTarArchiveRejectsPathTraversal(t *testing.T) {
	dest := t.TempDir()
	data := buildTestTar(t, map[string]string{"../escape.txt": "nope"})

	err := extractTarArchive(bytes.NewReader(data), dest)
	if err == nil {
		t.Fatal("extractTarArchive() error = nil, want a path-traversal error")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected escape.txt to not be written outside dest, stat err = %v", statErr)
	}
}

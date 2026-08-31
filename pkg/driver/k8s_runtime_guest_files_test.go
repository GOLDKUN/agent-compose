//go:build k8scompose

package driver

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	appconfig "agent-compose/pkg/config"
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

	if err := k8sExtractTarArchive(bytes.NewReader(data), dest); err != nil {
		t.Fatalf("k8sExtractTarArchive() error = %v", err)
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
	if err := k8sExtractTarArchive(bytes.NewReader(archive), dest); err != nil {
		t.Fatalf("k8sExtractTarArchive() error = %v", err)
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
	if err := k8sExtractTarArchive(bytes.NewReader(archive.Bytes()), dest); err != nil {
		t.Fatalf("k8sExtractTarArchive() error = %v", err)
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

func TestK8sGuestDirClearScriptScopesHomeToHostSnapshotEntries(t *testing.T) {
	r := &k8sRuntime{config: &appconfig.Config{GuestHomePath: "/root"}}

	t.Run("non-home guestDir wipes wholesale", func(t *testing.T) {
		script, homeEntries, err := r.k8sGuestDirClearScript(t.TempDir(), "/workspace")
		if err != nil {
			t.Fatalf("k8sGuestDirClearScript() error = %v", err)
		}
		if script != "rm -rf "+shellQuote("/workspace") {
			t.Fatalf("script = %q, want a wholesale rm -rf of /workspace", script)
		}
		if homeEntries != nil {
			t.Fatalf("homeEntries = %#v, want nil for a non-home guestDir (nothing to track a manifest for)", homeEntries)
		}
	})

	t.Run("home guestDir only clears the host snapshot's own entries", func(t *testing.T) {
		hostHome := t.TempDir()
		for _, name := range []string{".codex", ".claude"} {
			if err := os.MkdirAll(filepath.Join(hostHome, name), 0o755); err != nil {
				t.Fatalf("create host home entry %s: %v", name, err)
			}
		}
		script, homeEntries, err := r.k8sGuestDirClearScript(hostHome, "/root")
		if err != nil {
			t.Fatalf("k8sGuestDirClearScript() error = %v", err)
		}
		want := "rm -rf " + shellQuote("/root/.claude") + " " + shellQuote("/root/.codex")
		if script != want {
			t.Fatalf("script = %q, want %q (only entries the daemon actually manages, never a wholesale /root wipe that would delete image-baked content like .gitconfig or .local/bin)", script, want)
		}
		wantEntries := []string{".claude", ".codex"}
		if !slices.Equal(homeEntries, wantEntries) {
			t.Fatalf("homeEntries = %#v, want %#v", homeEntries, wantEntries)
		}
	})

	t.Run("empty host snapshot clears nothing", func(t *testing.T) {
		script, _, err := r.k8sGuestDirClearScript(t.TempDir(), "/root")
		if err != nil {
			t.Fatalf("k8sGuestDirClearScript() error = %v", err)
		}
		if script != "true" {
			t.Fatalf("script = %q, want a no-op when the daemon has never written anything to home", script)
		}
	})

	// Regression test: a top-level entry the daemon pushed in a previous
	// run (recorded in the push manifest) but has since stopped writing at
	// all must still be cleared from a Pod reused across runs, not left
	// stale forever just because it's absent from the current snapshot.
	t.Run("stale entry from a previous push is still cleared even though it's gone from the current snapshot", func(t *testing.T) {
		hostHome := t.TempDir()
		if err := os.MkdirAll(filepath.Join(hostHome, ".claude"), 0o755); err != nil {
			t.Fatalf("create host home entry: %v", err)
		}
		manifestPath := k8sHomePushManifestPath(hostHome)
		if err := k8sWriteHomePushManifest(manifestPath, []string{".claude", ".codex"}); err != nil {
			t.Fatalf("seed push manifest: %v", err)
		}

		script, homeEntries, err := r.k8sGuestDirClearScript(hostHome, "/root")
		if err != nil {
			t.Fatalf("k8sGuestDirClearScript() error = %v", err)
		}
		want := "rm -rf " + shellQuote("/root/.claude") + " " + shellQuote("/root/.codex")
		if script != want {
			t.Fatalf("script = %q, want %q (.codex is gone from the current snapshot but must still be cleared - it was pushed last time)", script, want)
		}
		// homeEntries (what gets recorded for next time) reflects the
		// current snapshot only - .codex is no longer part of what this
		// push restores, so it must not still be tracked afterward.
		wantEntries := []string{".claude"}
		if !slices.Equal(homeEntries, wantEntries) {
			t.Fatalf("homeEntries = %#v, want %#v (the manifest must drop entries no longer being pushed)", homeEntries, wantEntries)
		}
	})
}

func TestWriteGuestDirSkipsPushWhenGuestDirOverlapsVolumeMount(t *testing.T) {
	sandbox := testSandbox(t, "sandbox-volume-overlap")
	sandbox.VolumeMounts = []SandboxVolumeMount{
		{Type: "volume", Driver: RuntimeDriverK8s, Target: "/workspace", HostPath: "agent-compose/claim-workspace"},
	}
	// Zero-value runtime: if the overlap guard did not short-circuit before
	// EnsureSandbox, this would fail trying to build a real k8s client.
	runtime := &k8sRuntime{}
	if err := runtime.WriteGuestDir(context.Background(), sandbox, VMState{}, t.TempDir(), "/workspace"); err != nil {
		t.Fatalf("WriteGuestDir() error = %v, want nil (push into a PVC-mounted target must be skipped, not attempted)", err)
	}
}

func TestK8sGuestDirVolumeMountOverlapKind(t *testing.T) {
	sandbox := testSandbox(t, "sandbox-overlap-cases")
	sandbox.VolumeMounts = []SandboxVolumeMount{
		{Type: "volume", Driver: RuntimeDriverK8s, Target: "/workspace", HostPath: "agent-compose/claim-a"},
		{Type: "volume", Driver: RuntimeDriverK8s, Target: "/data/cache", HostPath: "agent-compose/claim-b"},
		{Type: "volume", Driver: RuntimeDriverK8s, Target: "/big-volume", HostPath: "agent-compose/claim-c"},
		{Type: "bind", Target: "/root", HostPath: "/host/root"},
	}
	cases := map[string]struct {
		guestDir string
		want     k8sGuestDirVolumeMountOverlap
	}{
		"exact match": {"/workspace", k8sGuestDirVolumeMountOverlapExact},
		// Partial overlaps are rejected at Pod-creation time by
		// k8sValidateVolumeMountTarget for any Pod this daemon creates (see
		// TestK8sValidateVolumeMountTarget), but this function still must
		// catch them at runtime too, as a safety net for a reused Pod whose
		// mounts were never validated (created before that check existed, or
		// out-of-band).
		"mount is a descendant of guestDir": {"/data", k8sGuestDirVolumeMountOverlapPartial},
		"guestDir is a descendant of mount": {"/big-volume/subdir", k8sGuestDirVolumeMountOverlapPartial},
		"no overlap":                        {"/root/.codex", k8sGuestDirVolumeMountOverlapNone},
		"non-k8s mount type ignored":        {"/root", k8sGuestDirVolumeMountOverlapNone},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := k8sGuestDirVolumeMountOverlapKind(sandbox, tc.guestDir); got != tc.want {
				t.Fatalf("k8sGuestDirVolumeMountOverlapKind(%q) = %v, want %v", tc.guestDir, got, tc.want)
			}
		})
	}
}

func TestExtractTarArchiveRejectsPathTraversal(t *testing.T) {
	dest := t.TempDir()
	data := buildTestTar(t, map[string]string{"../escape.txt": "nope"})

	err := k8sExtractTarArchive(bytes.NewReader(data), dest)
	if err == nil {
		t.Fatal("k8sExtractTarArchive() error = nil, want a path-traversal error")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected escape.txt to not be written outside dest, stat err = %v", statErr)
	}
}

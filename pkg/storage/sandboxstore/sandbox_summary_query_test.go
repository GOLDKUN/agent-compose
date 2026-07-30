package sandboxstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	appconfig "agent-compose/pkg/config"
)

func TestListSandboxSummariesUsesProjectionInOneBatch(t *testing.T) {
	store := newTestStore(t)
	first := seedSandboxDir(t, store, "sandbox-1", time.Unix(100, 0).UTC())
	second := seedSandboxDir(t, store, "sandbox-2", time.Unix(200, 0).UTC())
	store.recordIndex(first)
	store.recordIndex(second)
	for _, id := range []string{first.Summary.ID, second.Summary.ID} {
		if err := os.WriteFile(filepath.Join(store.sandboxDir(id), "metadata.json"), []byte("{"), 0o600); err != nil {
			t.Fatalf("corrupt projected sandbox metadata %s: %v", id, err)
		}
	}

	summaries, err := store.ListSandboxSummaries(context.Background(), []string{" sandbox-2 ", "sandbox-1", "sandbox-2", "missing"})
	if err != nil {
		t.Fatalf("list sandbox summaries: %v", err)
	}
	if len(summaries) != 2 || summaries["sandbox-1"].Title != first.Summary.Title || summaries["sandbox-2"].Title != second.Summary.Title {
		t.Fatalf("sandbox summaries = %#v", summaries)
	}
}

func TestListSandboxSummariesFilesystemFallbackReturnsPartialResults(t *testing.T) {
	root := t.TempDir()
	store := FromConfig(&appconfig.Config{SandboxRoot: root})
	valid := seedSandboxDir(t, store, "sandbox-valid", time.Unix(100, 0).UTC())
	brokenDir := store.sandboxDir("sandbox-broken")
	if err := os.MkdirAll(brokenDir, 0o755); err != nil {
		t.Fatalf("create broken sandbox dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(brokenDir, "metadata.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write broken sandbox metadata: %v", err)
	}

	summaries, err := store.ListSandboxSummaries(context.Background(), []string{"sandbox-valid", "sandbox-broken", "sandbox-valid"})
	if err == nil {
		t.Fatalf("expected partial fallback error")
	}
	if len(summaries) != 1 || summaries["sandbox-valid"].Title != valid.Summary.Title {
		t.Fatalf("partial sandbox summaries = %#v", summaries)
	}
}

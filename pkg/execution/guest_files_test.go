package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncHostFileToGuestUsesPersistedContent(t *testing.T) {
	hostPath := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(hostPath, []byte("persisted request"), 0o644); err != nil {
		t.Fatalf("write host file: %v", err)
	}
	var gotPath string
	var gotContent []byte
	err := SyncHostFileToGuest(context.Background(), hostPath, "/state/request.json", func(_ context.Context, guestPath string, content []byte) error {
		gotPath = guestPath
		gotContent = append([]byte(nil), content...)
		return nil
	})
	if err != nil {
		t.Fatalf("SyncHostFileToGuest returned error: %v", err)
	}
	if gotPath != "/state/request.json" || string(gotContent) != "persisted request" {
		t.Fatalf("guest write = %q %q", gotPath, gotContent)
	}
}

func TestGuestArtifactSyncOptionalCapabilitiesAndErrors(t *testing.T) {
	ctx := context.Background()
	if err := SyncHostFileToGuest(ctx, "/missing", "/guest", nil); err != nil {
		t.Fatalf("nil guest writer returned error: %v", err)
	}
	if err := SyncGuestDirToHost(ctx, "/guest", "/host", nil); err != nil {
		t.Fatalf("nil guest reader returned error: %v", err)
	}

	wantErr := errors.New("pull failed")
	err := SyncGuestDirToHost(ctx, "/guest/artifacts", "/host/artifacts", func(_ context.Context, guestDir, hostDir string) error {
		if guestDir != "/guest/artifacts" || hostDir != "/host/artifacts" {
			t.Fatalf("pull paths = %q, %q", guestDir, hostDir)
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("SyncGuestDirToHost error = %v, want %v", err, wantErr)
	}
}

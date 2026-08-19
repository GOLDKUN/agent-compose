package sandboxes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoverCommittedSandboxArchiveRequiresDirectorySync(t *testing.T) {
	archiveRoot := filepath.Join(t.TempDir(), "archives")
	const sandboxID = "durability-retry"
	const archiveID = "archive"
	writeRecoveryArchive(t, archiveRoot, sandboxID, archiveID)

	root, err := os.OpenRoot(archiveRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	directory, err := root.OpenRoot(sandboxID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = directory.Close() })

	failedHandle, err := directory.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	if err := failedHandle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, committed, err := recoverCommittedSandboxArchive(
		context.Background(), directory, failedHandle, sandboxArchiveIdentity{SandboxID: sandboxID, ArchiveID: archiveID},
	); err == nil || !committed {
		t.Fatalf("closed directory handle recovery = committed %v, error %v", committed, err)
	}

	retryHandle, err := directory.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = retryHandle.Close() }()
	manifest, committed, err := recoverCommittedSandboxArchive(
		context.Background(), directory, retryHandle, sandboxArchiveIdentity{SandboxID: sandboxID, ArchiveID: archiveID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !committed || manifest.SandboxID != sandboxID || manifest.ArchiveID != archiveID {
		t.Fatalf("durable recovery = committed %v, manifest %#v", committed, manifest)
	}
}

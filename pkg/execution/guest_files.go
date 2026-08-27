package execution

import (
	"context"
	"fmt"
	"os"
)

// GuestFileReaderFunc reads one file out of a running sandbox by its
// guest-absolute path. Drivers with a shared filesystem pass nil and keep
// using the corresponding daemon-local path.
type GuestFileReaderFunc func(ctx context.Context, guestPath string) ([]byte, error)

// GuestFileWriterFunc writes content into a running sandbox by its
// guest-absolute path. Drivers with a shared filesystem pass nil because the
// daemon-local write is already visible inside the sandbox.
type GuestFileWriterFunc func(ctx context.Context, guestPath string, content []byte) error

// GuestDirReaderFunc copies a guest directory into a daemon-local directory.
// A nil function means the runtime exposes the directory through a shared
// filesystem already.
type GuestDirReaderFunc func(ctx context.Context, guestDir, hostDestDir string) error

// GuestDirWriterFunc copies a daemon-local directory into a guest directory.
// A nil function means the runtime exposes the directory through a shared
// filesystem already.
type GuestDirWriterFunc func(ctx context.Context, hostSrcDir, guestDir string) error

// SyncHostFileToGuest pushes the exact daemon-side artifact after it has been
// persisted locally. It is a no-op for runtimes backed by shared mounts.
func SyncHostFileToGuest(ctx context.Context, hostPath, guestPath string, writeGuestFile GuestFileWriterFunc) error {
	if writeGuestFile == nil {
		return nil
	}
	content, err := os.ReadFile(hostPath)
	if err != nil {
		return fmt.Errorf("read host file %s: %w", hostPath, err)
	}
	if err := writeGuestFile(ctx, guestPath, content); err != nil {
		return fmt.Errorf("write guest file %s: %w", guestPath, err)
	}
	return nil
}

// SyncGuestDirToHost pulls artifacts produced by an Exec call back to the
// daemon. It is a no-op for runtimes backed by shared mounts.
func SyncGuestDirToHost(ctx context.Context, guestDir, hostDestDir string, readGuestDir GuestDirReaderFunc) error {
	if readGuestDir == nil {
		return nil
	}
	if err := readGuestDir(ctx, guestDir, hostDestDir); err != nil {
		return fmt.Errorf("read guest directory %s: %w", guestDir, err)
	}
	return nil
}

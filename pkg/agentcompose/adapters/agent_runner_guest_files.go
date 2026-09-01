package adapters

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chaitin/agent-compose/pkg/execution"
	domain "github.com/chaitin/agent-compose/pkg/model"
)

// guestFileReaderFor returns a pull function for runtimes without a shared
// filesystem. A nil result tells callers to use the existing mounted path.
func (r *AgentRunner) guestFileReaderFor(session *domain.Sandbox) execution.GuestFileReaderFunc {
	if r == nil || r.runtimes == nil || r.store == nil {
		return nil
	}
	runtime, err := r.runtimes.ForSession(session)
	if err != nil {
		return nil
	}
	reader, ok := runtime.(GuestFileReader)
	if !ok {
		return nil
	}
	vmState, err := r.store.GetVMState(session.Summary.ID)
	if err != nil {
		return nil
	}
	return func(ctx context.Context, guestPath string) ([]byte, error) {
		return reader.ReadGuestFile(ctx, session, vmState, guestPath)
	}
}

func (r *AgentRunner) guestFileWriterFor(session *domain.Sandbox) execution.GuestFileWriterFunc {
	if r == nil || r.runtimes == nil || r.store == nil {
		return nil
	}
	runtime, err := r.runtimes.ForSession(session)
	if err != nil {
		return nil
	}
	writer, ok := runtime.(GuestFileWriter)
	if !ok {
		return nil
	}
	vmState, err := r.store.GetVMState(session.Summary.ID)
	if err != nil {
		return nil
	}
	return func(ctx context.Context, guestPath string, content []byte) error {
		return writer.WriteGuestFile(ctx, session, vmState, guestPath, content)
	}
}

func (r *AgentRunner) guestDirWriterFor(session *domain.Sandbox) execution.GuestDirWriterFunc {
	if r == nil || r.runtimes == nil || r.store == nil {
		return nil
	}
	runtime, err := r.runtimes.ForSession(session)
	if err != nil {
		return nil
	}
	writer, ok := runtime.(GuestDirWriter)
	if !ok {
		return nil
	}
	vmState, err := r.store.GetVMState(session.Summary.ID)
	if err != nil {
		return nil
	}
	return func(ctx context.Context, hostSrcDir, guestDir string) error {
		return writer.WriteGuestDir(ctx, session, vmState, hostSrcDir, guestDir)
	}
}

// syncSandboxGuestDirectories transfers the daemon-owned workspace and home
// snapshots after provisioning and runtime config generation. Drivers with
// shared mounts do not expose GuestDirWriter, so they keep their existing path.
func (r *AgentRunner) syncSandboxGuestDirectories(ctx context.Context, session *domain.Sandbox) error {
	writeGuestDir := r.guestDirWriterFor(session)
	if writeGuestDir == nil {
		return nil
	}
	directories := []struct {
		name      string
		hostPath  string
		guestPath string
	}{
		{name: "workspace", hostPath: session.Summary.WorkspacePath, guestPath: r.config.GuestWorkspacePath},
		{name: "home", hostPath: execution.HostSandboxHome(session), guestPath: r.config.GuestHomePath},
	}
	for _, directory := range directories {
		info, err := os.Stat(directory.hostPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect sandbox %s directory %s: %w", directory.name, directory.hostPath, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("sandbox %s path %s is not a directory", directory.name, directory.hostPath)
		}
		if strings.TrimSpace(directory.guestPath) == "" {
			return fmt.Errorf("guest %s path is required", directory.name)
		}
		if err := writeGuestDir(ctx, filepath.Clean(directory.hostPath), filepath.Clean(directory.guestPath)); err != nil {
			return fmt.Errorf("push sandbox %s to guest: %w", directory.name, err)
		}
	}
	return nil
}

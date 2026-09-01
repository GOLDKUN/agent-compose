package adapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/storage/sandboxstore"
)

type guestDirWriteCall struct {
	hostPath  string
	guestPath string
}

type capturingGuestDirRuntime struct {
	fakeAgentRuntime
	writes []guestDirWriteCall
	err    error
}

func (r *capturingGuestDirRuntime) WriteGuestDir(_ context.Context, _ *domain.Sandbox, _ domain.VMState, hostPath, guestPath string) error {
	r.writes = append(r.writes, guestDirWriteCall{hostPath: hostPath, guestPath: guestPath})
	return r.err
}

func TestAgentRunnerSyncsWorkspaceAndHomeForGuestDirRuntime(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:           root,
		SandboxRoot:        filepath.Join(root, "sandboxes"),
		RuntimeDriver:      driverpkg.RuntimeDriverK8s,
		GuestWorkspacePath: "/workspace",
		GuestHomePath:      "/root",
	}
	store, err := sandboxstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSandbox(ctx, "k8s sync", "", driverpkg.RuntimeDriverK8s, "guest:latest", "", domain.SandboxTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSandbox returned error: %v", err)
	}
	if err := os.MkdirAll(execution.HostSandboxHome(session), 0o755); err != nil {
		t.Fatalf("create sandbox home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(session.Summary.WorkspacePath, "README.md"), []byte("workspace"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(execution.HostSandboxHome(session), ".profile"), []byte("profile"), 0o600); err != nil {
		t.Fatalf("write home file: %v", err)
	}
	runtime := &capturingGuestDirRuntime{}
	runner := NewAgentRunner(AgentRunnerDeps{
		Config: config, Store: store, Runtimes: fakeRuntimeProvider{runtime: runtime},
	})

	if err := runner.syncSandboxGuestDirectories(ctx, session); err != nil {
		t.Fatalf("syncSandboxGuestDirectories returned error: %v", err)
	}
	want := []guestDirWriteCall{
		{hostPath: filepath.Clean(session.Summary.WorkspacePath), guestPath: "/workspace"},
		{hostPath: filepath.Clean(execution.HostSandboxHome(session)), guestPath: "/root"},
	}
	if !reflect.DeepEqual(runtime.writes, want) {
		t.Fatalf("guest directory writes = %#v, want %#v", runtime.writes, want)
	}

	runtime.writes = nil
	runtime.err = errors.New("push failed")
	err = runner.syncSandboxGuestDirectories(ctx, session)
	if err == nil || !strings.Contains(err.Error(), "push sandbox workspace to guest") || !errors.Is(err, runtime.err) {
		t.Fatalf("sync error = %v, want contextual workspace push error", err)
	}
}

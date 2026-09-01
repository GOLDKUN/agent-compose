package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "github.com/chaitin/agent-compose/pkg/config"
	driverpkg "github.com/chaitin/agent-compose/pkg/driver"
	domain "github.com/chaitin/agent-compose/pkg/model"
	"github.com/chaitin/agent-compose/pkg/sandboxes"
	"github.com/chaitin/agent-compose/pkg/storage/sandboxstore"
)

type guestFileSchedulerCommandRuntime struct {
	fakeSchedulerCommandRuntime
	writtenPath string
	writtenData []byte
	execStarted bool
	pulledDir   string
}

func (r *guestFileSchedulerCommandRuntime) WriteGuestFile(_ context.Context, _ *domain.Sandbox, _ domain.VMState, guestPath string, content []byte) error {
	if r.execStarted {
		return os.ErrInvalid
	}
	r.writtenPath = guestPath
	r.writtenData = append([]byte(nil), content...)
	return nil
}

func (r *guestFileSchedulerCommandRuntime) ExecStream(ctx context.Context, sandbox *domain.Sandbox, state domain.VMState, spec domain.ExecSpec, stream domain.ExecStreamWriter) (domain.ExecResult, error) {
	r.execStarted = true
	return r.fakeSchedulerCommandRuntime.ExecStream(ctx, sandbox, state, spec, stream)
}

func (r *guestFileSchedulerCommandRuntime) ReadGuestDir(_ context.Context, _ *domain.Sandbox, _ domain.VMState, guestDir, hostDestDir string) error {
	if !r.execStarted {
		return os.ErrInvalid
	}
	r.pulledDir = guestDir
	return os.WriteFile(filepath.Join(hostDestDir, "command-result.json"), []byte("{\"success\":true}\n"), 0o644)
}

func TestSchedulerCommandExecutorTransfersGuestRequestAndArtifacts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:            root,
		SandboxRoot:         filepath.Join(root, "sandboxes"),
		RuntimeDriver:       driverpkg.RuntimeDriverK8s,
		DefaultImage:        "guest:latest",
		GuestWorkspacePath:  "/workspace",
		GuestStateRoot:      "/state",
		GuestHomePath:       "/root",
		SandboxStartTimeout: 2 * time.Second,
	}
	store, err := sandboxstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSandbox(ctx, "k8s scheduler command", "", driverpkg.RuntimeDriverK8s, "guest:latest", "", domain.SandboxTypeScript, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSandbox returned error: %v", err)
	}
	session.Summary.VMStatus = domain.VMStatusRunning
	if err := store.UpdateSandbox(ctx, session); err != nil {
		t.Fatalf("UpdateSandbox returned error: %v", err)
	}
	runtime := &guestFileSchedulerCommandRuntime{}
	executor := NewSchedulerCommandExecutor(SchedulerCommandExecutorDeps{
		Config: config, Store: store, Runtimes: fakeRuntimeProvider{runtime: runtime}, Streams: sandboxes.NewStreamBrokerForTest(),
	})
	result, err := executor.ExecuteSchedulerCommand(ctx, session, domain.SchedulerCommandRequest{Mode: "shell", Script: "echo scheduler"})
	if err != nil {
		t.Fatalf("ExecuteSchedulerCommand returned error: %v", err)
	}
	if !strings.HasPrefix(runtime.writtenPath, "/state/cells/") || !strings.HasSuffix(runtime.writtenPath, "/command-request.json") {
		t.Fatalf("guest request path = %q", runtime.writtenPath)
	}
	if !strings.Contains(string(runtime.writtenData), "echo scheduler") {
		t.Fatalf("guest request content = %q", runtime.writtenData)
	}
	if runtime.pulledDir != filepath.Dir(runtime.writtenPath) {
		t.Fatalf("pulled guest dir = %q, want %q", runtime.pulledDir, filepath.Dir(runtime.writtenPath))
	}
	if data, readErr := os.ReadFile(result.Artifacts["result"]); readErr != nil || !strings.Contains(string(data), `"success":true`) {
		t.Fatalf("pulled result artifact = %q, err = %v", data, readErr)
	}
}

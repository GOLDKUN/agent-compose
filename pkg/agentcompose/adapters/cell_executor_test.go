package adapters

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "github.com/chaitin/agent-compose/pkg/config"
	driverpkg "github.com/chaitin/agent-compose/pkg/driver"
	"github.com/chaitin/agent-compose/pkg/execution"
	domain "github.com/chaitin/agent-compose/pkg/model"
	"github.com/chaitin/agent-compose/pkg/storage/sandboxstore"
)

type fakeCellRuntime struct {
	result domain.ExecResult
}

type guestFileCellRuntime struct {
	fakeCellRuntime
	writtenPath    string
	writtenContent []byte
	execStarted    bool
}

func (r fakeCellRuntime) EnsureSandbox(context.Context, *domain.Sandbox, domain.VMState, domain.ProxyState) (domain.SandboxVMInfo, error) {
	return domain.SandboxVMInfo{}, nil
}

func (r fakeCellRuntime) StopSandbox(context.Context, *domain.Sandbox, domain.VMState) (bool, error) {
	return false, nil
}

func (r fakeCellRuntime) RemoveSandbox(context.Context, *domain.Sandbox, domain.VMState) error {
	return nil
}

func (r fakeCellRuntime) Exec(context.Context, *domain.Sandbox, domain.VMState, domain.ExecSpec) (domain.ExecResult, error) {
	return r.result, nil
}

func (r fakeCellRuntime) ExecStream(_ context.Context, _ *domain.Sandbox, _ domain.VMState, _ domain.ExecSpec, stream domain.ExecStreamWriter) (domain.ExecResult, error) {
	if stream != nil {
		stream(domain.ExecChunk{Text: r.result.Stdout})
	}
	return r.result, nil
}

func (r *guestFileCellRuntime) WriteGuestFile(_ context.Context, _ *domain.Sandbox, _ domain.VMState, guestPath string, content []byte) error {
	if r.execStarted {
		return errors.New("cell script was pushed after exec started")
	}
	r.writtenPath = guestPath
	r.writtenContent = append([]byte(nil), content...)
	return nil
}

func (r *guestFileCellRuntime) ExecStream(ctx context.Context, sandbox *domain.Sandbox, state domain.VMState, spec domain.ExecSpec, stream domain.ExecStreamWriter) (domain.ExecResult, error) {
	r.execStarted = true
	return r.fakeCellRuntime.ExecStream(ctx, sandbox, state, spec, stream)
}

func TestCellExecutorExecuteCellPersistsCellAndEvent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:             root,
		SandboxRoot:          filepath.Join(root, "sandboxes"),
		RuntimeDriver:        driverpkg.RuntimeDriverBoxlite,
		DefaultImage:         "guest:latest",
		GuestWorkspacePath:   "/workspace",
		GuestStateRoot:       "/state",
		JupyterProxyBasePath: "/agent-compose/session",
		SandboxStartTimeout:  2 * time.Second,
	}
	store, err := sandboxstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSandbox(ctx, "cell session", "", driverpkg.RuntimeDriverBoxlite, "guest:latest", "", domain.SandboxTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	session.Summary.VMStatus = domain.VMStatusRunning
	if err := store.UpdateSandbox(ctx, session); err != nil {
		t.Fatalf("UpdateSession returned error: %v", err)
	}
	executor := NewCellExecutor(config, store, fakeRuntimeProvider{runtime: fakeCellRuntime{result: domain.ExecResult{
		Stdout:   "hello\n",
		Output:   "hello\n",
		ExitCode: 0,
		Success:  true,
	}}}, nil)

	cell, err := executor.ExecuteCell(ctx, session, execution.CellTypeShell, "echo hello")
	if err != nil {
		t.Fatalf("ExecuteCell returned error: %v", err)
	}
	if !cell.Success || cell.Stdout != "hello\n" {
		t.Fatalf("cell = %#v", cell)
	}
	cells, err := store.ListCells(ctx, session.Summary.ID)
	if err != nil {
		t.Fatalf("ListCells returned error: %v", err)
	}
	if len(cells) != 1 || cells[0].ID != cell.ID {
		t.Fatalf("stored cells = %#v", cells)
	}
	events, err := store.ListEvents(ctx, session.Summary.ID)
	if err != nil {
		t.Fatalf("ListEvents returned error: %v", err)
	}
	if len(events) != 1 || events[0].Type != "kernel.cell.succeeded" {
		t.Fatalf("events = %#v", events)
	}

	var started bool
	var chunks []domain.ExecChunk
	streamed, err := executor.ExecuteCellStream(ctx, CellExecutionRequest{
		Session:  session,
		CellType: execution.CellTypePython,
		Source:   "print('hello')",
		Stream: execution.CellExecutionStream{
			OnStart: func(cell domain.NotebookCell) error {
				started = cell.Running && cell.Type == execution.CellTypePython
				return nil
			},
			OnChunk: func(_ string, chunk domain.ExecChunk) error {
				chunks = append(chunks, chunk)
				return nil
			},
		},
	})
	if err != nil || !streamed.Success || !started || len(chunks) != 1 {
		t.Fatalf("ExecuteCellStream cell=%#v started=%v chunks=%#v err=%v", streamed, started, chunks, err)
	}
	cells, err = store.ListCells(ctx, session.Summary.ID)
	if err != nil || len(cells) != 2 {
		t.Fatalf("streamed cells=%#v err=%v", cells, err)
	}

	if _, err := executor.ExecuteCellStream(ctx, CellExecutionRequest{
		Session:  session,
		CellType: execution.CellTypeShell,
		Source:   "echo hello",
		Stream: execution.CellExecutionStream{
			OnStart: func(domain.NotebookCell) error {
				return errors.New("start callback failed")
			},
		},
	}); err == nil {
		t.Fatalf("ExecuteCellStream start callback returned nil error")
	}
	if _, err := executor.ExecuteCellStream(ctx, CellExecutionRequest{
		Session:  session,
		CellType: execution.CellTypeShell,
		Source:   "echo hello",
		Stream: execution.CellExecutionStream{
			OnChunk: func(string, domain.ExecChunk) error {
				return errors.New("chunk callback failed")
			},
		},
	}); err == nil {
		t.Fatalf("ExecuteCellStream chunk callback returned nil error")
	}

	failingExecutor := NewCellExecutor(config, store, fakeRuntimeProvider{runtime: fakeCellRuntime{result: domain.ExecResult{
		Stderr:   "boom",
		Output:   "boom",
		ExitCode: 9,
		Success:  false,
	}}}, nil)
	failedCell, err := failingExecutor.ExecuteCell(ctx, session, execution.CellTypeShell, "exit 9")
	if err != nil || failedCell.Success || failedCell.ExitCode != 9 {
		t.Fatalf("failed ExecuteCell cell=%#v err=%v", failedCell, err)
	}
	events, err = store.ListEvents(ctx, session.Summary.ID)
	if err != nil || events[len(events)-1].Type != "kernel.cell.failed" {
		t.Fatalf("failed events=%#v err=%v", events, err)
	}
}

func TestCellExecutorPushesScriptBeforeGuestExec(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:            root,
		SandboxRoot:         filepath.Join(root, "sandboxes"),
		RuntimeDriver:       driverpkg.RuntimeDriverK8s,
		DefaultImage:        "guest:latest",
		GuestWorkspacePath:  "/workspace",
		GuestStateRoot:      "/state",
		SandboxStartTimeout: 2 * time.Second,
	}
	store, err := sandboxstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSandbox(ctx, "k8s cell", "", driverpkg.RuntimeDriverK8s, "guest:latest", "", domain.SandboxTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSandbox returned error: %v", err)
	}
	runtime := &guestFileCellRuntime{fakeCellRuntime: fakeCellRuntime{result: domain.ExecResult{Stdout: "ok\n", Output: "ok\n", Success: true}}}
	executor := NewCellExecutor(config, store, fakeRuntimeProvider{runtime: runtime}, nil)
	if _, err := executor.ExecuteCell(ctx, session, execution.CellTypeShell, "echo ok"); err != nil {
		t.Fatalf("ExecuteCell returned error: %v", err)
	}
	if !runtime.execStarted || !strings.HasPrefix(runtime.writtenPath, "/state/cells/") || !strings.HasSuffix(runtime.writtenPath, "/cell.sh") {
		t.Fatalf("guest script path = %q, exec started = %v", runtime.writtenPath, runtime.execStarted)
	}
	if string(runtime.writtenContent) != "echo ok" {
		t.Fatalf("guest script content = %q", runtime.writtenContent)
	}
}

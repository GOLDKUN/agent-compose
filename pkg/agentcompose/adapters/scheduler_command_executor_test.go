package adapters

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sandboxes"
	"agent-compose/pkg/storage/sandboxstore"
)

type fakeSchedulerCommandRuntime struct{}

func (r fakeSchedulerCommandRuntime) EnsureSandbox(context.Context, *domain.Sandbox, domain.VMState, domain.ProxyState) (domain.SandboxVMInfo, error) {
	return domain.SandboxVMInfo{}, nil
}

func (r fakeSchedulerCommandRuntime) StopSandbox(context.Context, *domain.Sandbox, domain.VMState) (bool, error) {
	return false, nil
}

func (r fakeSchedulerCommandRuntime) RemoveSandbox(context.Context, *domain.Sandbox, domain.VMState) error {
	return nil
}

func (r fakeSchedulerCommandRuntime) Exec(context.Context, *domain.Sandbox, domain.VMState, domain.ExecSpec) (domain.ExecResult, error) {
	return domain.ExecResult{}, nil
}

func (r fakeSchedulerCommandRuntime) ExecStream(_ context.Context, _ *domain.Sandbox, _ domain.VMState, _ domain.ExecSpec, stream domain.ExecStreamWriter) (domain.ExecResult, error) {
	commandResult := domain.RuntimeCommandResult{
		Stdout:   "scheduler stdout\n",
		Stderr:   "scheduler stderr\n",
		Output:   "scheduler stdout\nscheduler stderr\n",
		ExitCode: 0,
		Success:  true,
	}
	payloadBytes, _ := json.Marshal(commandResult)
	payload := execution.CommandResultPrefix + string(payloadBytes) + "\n"
	if stream != nil {
		stream(domain.ExecChunk{Text: "scheduler stdout\n"})
		stream(domain.ExecChunk{Text: "scheduler stderr\n", Stream: domain.StdioStderr})
		stream(domain.ExecChunk{Text: payload})
	}
	return domain.ExecResult{
		Stdout:   "scheduler stdout\n" + payload,
		Stderr:   "scheduler stderr\n",
		Output:   "scheduler stdout\nscheduler stderr\n" + payload,
		ExitCode: 0,
		Success:  true,
	}, nil
}

func TestSchedulerCommandExecutorFiltersCommandPayloadFromStreamingCellOutput(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:             root,
		SandboxRoot:          filepath.Join(root, "sandboxes"),
		RuntimeDriver:        driverpkg.RuntimeDriverBoxlite,
		DefaultImage:         "guest:latest",
		GuestWorkspacePath:   "/workspace",
		GuestStateRoot:       "/data/state",
		GuestHomePath:        "/root",
		JupyterProxyBasePath: "/agent-compose/session",
		SandboxStartTimeout:  2 * time.Second,
	}
	store, err := sandboxstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	session, err := store.CreateSandbox(ctx, "scheduler command sandbox", "", driverpkg.RuntimeDriverBoxlite, "guest:latest", "", domain.SandboxTypeScript, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	session.Summary.VMStatus = domain.VMStatusRunning
	if err := store.UpdateSandbox(ctx, session); err != nil {
		t.Fatalf("UpdateSession returned error: %v", err)
	}
	streams := sandboxes.NewStreamBrokerForTest()
	ch, unsubscribe := streams.Subscribe(session.Summary.ID)
	defer unsubscribe()
	executor := NewSchedulerCommandExecutor(config, store, nil, fakeRuntimeProvider{runtime: fakeSchedulerCommandRuntime{}}, streams)

	result, err := executor.ExecuteSchedulerCommand(ctx, session, domain.SchedulerCommandRequest{
		Mode:   "shell",
		Script: "echo scheduler",
	})
	if err != nil {
		t.Fatalf("ExecuteSchedulerCommand returned error: %v", err)
	}
	if !result.Success || result.Stdout != "scheduler stdout\n" || result.Stderr != "scheduler stderr\n" {
		t.Fatalf("scheduler result = %#v", result)
	}

	var outputText strings.Builder
	for {
		select {
		case event := <-ch:
			if event.EventType == sandboxes.WatchEventTypeCellOutput {
				outputText.WriteString(event.Chunk)
				if strings.Contains(event.Chunk, execution.CommandResultPrefix) {
					t.Fatalf("stream event leaked command payload: %#v", event)
				}
			}
		default:
			goto drained
		}
	}

drained:
	if got := outputText.String(); !strings.Contains(got, "scheduler stdout\n") || !strings.Contains(got, "scheduler stderr\n") {
		t.Fatalf("stream output = %q", got)
	}
	cells, err := store.ListCells(ctx, session.Summary.ID)
	if err != nil {
		t.Fatalf("ListCells returned error: %v", err)
	}
	if len(cells) == 0 {
		t.Fatalf("no cells stored")
	}
	for _, cell := range cells {
		if strings.Contains(cell.Output, execution.CommandResultPrefix) {
			t.Fatalf("cell leaked command payload: %#v", cell)
		}
	}
}

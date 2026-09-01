package runs

import (
	"context"
	"path/filepath"
	"testing"

	appconfig "github.com/chaitin/agent-compose/pkg/config"
	driverpkg "github.com/chaitin/agent-compose/pkg/driver"
	domain "github.com/chaitin/agent-compose/pkg/model"
	"github.com/chaitin/agent-compose/pkg/storage/sandboxstore"
)

func TestRunCommandInteractionMissingDependenciesPreservesLogsPath(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := &appconfig.Config{
		DataRoot:           root,
		SandboxRoot:        filepath.Join(root, "sandboxes"),
		RuntimeDriver:      driverpkg.RuntimeDriverDocker,
		DefaultImage:       "guest:latest",
		DockerDefaultImage: "guest:latest",
		GuestStateRoot:     "/guest/state",
	}
	store, err := sandboxstore.NewWithConfig(config)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	sandbox, err := store.CreateSandbox(ctx, "attach session", "", driverpkg.RuntimeDriverDocker, "guest:latest", "", domain.SandboxTypeManual, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSandbox returned error: %v", err)
	}
	run := domain.ProjectRunRecord{RunID: "run-attach-edge", ProjectID: "project-1", AgentName: "worker"}
	wantLogsPath := filepath.Join(projectRunCommandArtifactsDir(run, sandbox), "transcript.txt")

	// A bare Controller has no store/runtime configured, so
	// openCommandInteraction fails before opening an interaction. The
	// returned transition must still carry LogsPath: it is persisted via
	// completeProjectRunError and sent to the client via
	// runAttachResultResponse, matching runPromptInteraction's error path.
	transition, execErr := (&Controller{config: config}).runCommandInteraction(ctx, interactionRunContext{
		Run:     run,
		Sandbox: sandbox,
		Request: RunAgentRequest{Command: "echo hi"},
	}, RunAttachInput{}, nil, nil)
	if execErr == nil {
		t.Fatalf("runCommandInteraction with missing dependencies returned nil error")
	}
	if transition.LogsPath != wantLogsPath {
		t.Fatalf("transition.LogsPath = %q, want %q", transition.LogsPath, wantLogsPath)
	}
	if transition.ExitCode != 1 {
		t.Fatalf("transition.ExitCode = %d, want 1", transition.ExitCode)
	}
	if transition.Error == "" {
		t.Fatalf("transition.Error is empty, want a message")
	}
}

package e2e

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/klauspost/compress/zstd"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sandboxes"
	"agent-compose/pkg/storage/sandboxstore"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
	"agent-compose/proto/agentcompose/v2/agentcomposev2connect"
)

const dockerRetentionArchiveE2EImageEnv = "AGENT_COMPOSE_E2E_DOCKER_RETENTION_IMAGE"

func TestStoppedSandboxArchiveRecoveryLifecycle(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sandboxRoot := filepath.Join(root, "sandboxes")
	archiveRoot := filepath.Join(root, "archives")
	store, err := sandboxstore.NewWithConfig(&appconfig.Config{
		DataRoot: root, SandboxRoot: sandboxRoot, RuntimeDriver: "docker",
		DefaultImage: "guest:latest", DockerDefaultImage: "guest:latest", JupyterProxyBasePath: "/jupyter",
	})
	if err != nil {
		t.Fatal(err)
	}
	sandbox, err := store.CreateSandbox(ctx, "archive-recovery", "", "docker", "guest:latest", "", "test", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sandbox.Summary.VMStatus = domain.VMStatusStopped
	if err := store.UpdateSandbox(ctx, sandbox); err != nil {
		t.Fatal(err)
	}
	vmState, err := store.GetVMState(sandbox.Summary.ID)
	if err != nil {
		t.Fatal(err)
	}
	vmState.StoppedAt = time.Now().UTC().Add(-time.Hour)
	if err := store.SaveVMState(sandbox.Summary.ID, vmState); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.SandboxDir(sandbox.Summary.ID), "state", "recovery.txt"), []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}

	locks := sandboxes.NewLifecycleLocks()
	cleaner := &sandboxes.SandboxRetentionCleaner{
		Store: store, Locks: locks, SandboxRoot: sandboxRoot, ArchiveRoot: archiveRoot,
		Removal: retentionArchiveFailedRemoval{},
	}
	if _, err := cleaner.Clean(ctx, time.Now().UTC()); err == nil {
		t.Fatal("cleanup succeeded despite injected post-archive removal failure")
	}
	archives, err := filepath.Glob(filepath.Join(archiveRoot, sandbox.Summary.ID, "*.tar.zst"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("archives after interrupted removal = %v, error %v", archives, err)
	}
	if _, err := os.Stat(store.SandboxDir(sandbox.Summary.ID)); err != nil {
		t.Fatalf("interrupted removal did not retain sandbox for recovery: %v", err)
	}

	recovery := sandboxes.NewDeletionRecoveryWithArchiveRoot(&sandboxes.RemovalCoordinator{
		SandboxRoot: sandboxRoot, Store: store, Runtime: retentionArchiveRuntime{}, Locks: locks,
	}, archiveRoot, nil)
	if err := recovery.Start(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := store.GetSandbox(ctx, sandbox.Summary.ID); errors.Is(err, os.ErrNotExist) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := store.GetSandbox(ctx, sandbox.Summary.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup recovery left archived sandbox original: %v", err)
	}
	if err := recovery.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archives[0]); err != nil {
		t.Fatalf("startup recovery removed durable archive: %v", err)
	}
}

type retentionArchiveFailedRemoval struct{}

func (retentionArchiveFailedRemoval) Remove(context.Context, string, bool) (sandboxes.RemovalResult, error) {
	return sandboxes.RemovalResult{}, errors.New("injected removal interruption")
}

type retentionArchiveRuntime struct{}

func (retentionArchiveRuntime) StopSandboxVM(context.Context, *domain.Sandbox) error { return nil }
func (retentionArchiveRuntime) RemoveSandboxVM(context.Context, *domain.Sandbox) error {
	return nil
}

func TestE2EDockerStoppedSandboxRetentionArchivesAuditData(t *testing.T) {
	image := strings.TrimSpace(os.Getenv(dockerRetentionArchiveE2EImageEnv))
	if image == "" {
		t.Skipf("set %s to a local Docker guest image", dockerRetentionArchiveE2EImageEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	repoRoot := e2eRepoRoot(t)
	testRoot, err := os.MkdirTemp(repoRoot, ".docker-retention-e2e-")
	if err != nil {
		t.Fatalf("create Docker-visible E2E root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(testRoot) })
	dockerClient := newE2EDockerClient(t, ctx, image)
	binary := e2eDaemonBinary(t, ctx, repoRoot, testRoot)
	archiveRoot := filepath.Join(testRoot, "archives")
	listenAddress := unusedLoopbackAddress(t)
	baseURL := "http://" + listenAddress
	daemon := startE2EDaemonWithEnv(t, binary, repoRoot, testRoot, listenAddress, image, map[string]string{
		"SANDBOX_RETENTION_TTL": "1ns",
		"CLEANUP_INTERVAL":      "100ms",
		"SANDBOX_ARCHIVE_ROOT":  archiveRoot,
	})
	waitForE2EDaemon(t, ctx, daemon, baseURL)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("agent-compose daemon log:\n%s", daemon.logs.String())
		}
	})

	httpClient := newE2EHTTPClient()
	defer httpClient.CloseIdleConnections()
	projectClient := agentcomposev2connect.NewProjectServiceClient(httpClient, baseURL)
	runClient := agentcomposev2connect.NewRunServiceClient(httpClient, baseURL)
	sandboxClient := agentcomposev2connect.NewSandboxServiceClient(httpClient, baseURL)

	sourceRoot := filepath.Join(testRoot, "workspace-source")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeE2EHostFile(t, filepath.Join(sourceRoot, "source.txt"), "source")
	projectResponse, err := projectClient.ApplyProject(ctx, connect.NewRequest(&agentcomposev2.ApplyProjectRequest{
		Spec: &agentcomposev2.ProjectSpec{
			Name: "docker-retention-archive-e2e",
			Workspaces: []*agentcomposev2.NamedWorkspaceSpec{{
				Name: "source", Workspace: &agentcomposev2.WorkspaceSpec{Provider: "file", Path: "."},
			}},
			Agents: []*agentcomposev2.AgentSpec{{
				Name: "worker", Provider: "codex", Image: image,
				Driver:    &agentcomposev2.DriverSpec{Name: "docker", Config: &agentcomposev2.DriverSpec_Docker{Docker: &agentcomposev2.DockerDriverSpec{}}},
				Workspace: &agentcomposev2.WorkspaceSpec{Name: "source"},
			}},
		},
		Source: &agentcomposev2.ProjectSource{ComposePath: filepath.Join(sourceRoot, "agent-compose.yml"), ProjectDir: sourceRoot},
	}))
	if err != nil {
		t.Fatalf("ApplyProject: %v", err)
	}
	projectID := projectResponse.Msg.GetProject().GetSummary().GetProjectId()
	sandbox := runE2EWorkspaceSandbox(t, ctx, runClient, sandboxClient, projectID, "retention-archive")
	sandboxID := sandbox.GetSandboxId()
	removed := false
	t.Cleanup(func() { cleanupE2EWorkspaceSandbox(t, dockerClient, sandboxClient, sandboxID, removed) })
	if handle := inspectE2EDockerSandbox(t, ctx, dockerClient, sandboxID); !handle.Running {
		t.Fatalf("Docker sandbox is not running before stop: %+v", handle)
	}

	sandboxDir := filepath.Dir(sandbox.GetWorkspacePath())
	markers := map[string]string{
		"state/retention-state.txt": "state",
		"home/retention-home.txt":   "home",
		"logs/retention-log.txt":    "logs",
		"context/retention.txt":     "context",
		"runtime/retention.txt":     "runtime",
	}
	for name, contents := range markers {
		if err := os.WriteFile(filepath.Join(sandboxDir, filepath.FromSlash(name)), []byte(contents), 0o600); err != nil {
			t.Fatalf("write sandbox marker %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(sandbox.GetWorkspacePath(), "excluded.txt"), []byte("workspace"), 0o600); err != nil {
		t.Fatal(err)
	}

	stopResponse, err := sandboxClient.StopSandbox(ctx, connect.NewRequest(&agentcomposev2.StopSandboxRequest{SandboxId: sandboxID}))
	if err != nil {
		t.Fatalf("StopSandbox: %v", err)
	}
	if stopResponse.Msg.GetSandbox().GetStatus() != agentcomposev2.SandboxStatus_SANDBOX_STATUS_STOPPED {
		t.Fatalf("stopped sandbox = %#v", stopResponse.Msg.GetSandbox())
	}

	var archivePath string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		archives, _ := filepath.Glob(filepath.Join(archiveRoot, sandboxID, "*.tar.zst"))
		_, sandboxErr := os.Stat(sandboxDir)
		_, getErr := sandboxClient.GetSandbox(ctx, connect.NewRequest(&agentcomposev2.GetSandboxRequest{SandboxId: sandboxID}))
		if len(archives) == 1 && errors.Is(sandboxErr, os.ErrNotExist) && connect.CodeOf(getErr) == connect.CodeNotFound {
			archivePath = archives[0]
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if archivePath == "" {
		t.Fatalf("sandbox was not fully archived and removed\ndaemon log:\n%s", daemon.logs.String())
	}
	removed = true
	assertE2EDockerSandboxContainerCount(t, ctx, dockerClient, sandboxID, 0)
	entries := readE2ERetentionArchive(t, archivePath)
	for name, want := range markers {
		if got := entries["sandbox/"+name]; got != want {
			t.Fatalf("archive entry %s = %q, want %q", name, got, want)
		}
	}
	if _, exists := entries["sandbox/workspace/excluded.txt"]; exists {
		t.Fatal("workspace was included in retention archive")
	}
	for _, name := range []string{"sandbox/metadata.json", "sandbox/vm/runtime.json", "sandbox/proxy/jupyter.json", ".lifecycle/ownership.json"} {
		if _, exists := entries[name]; !exists {
			t.Fatalf("complete archive missing %s", name)
		}
	}
	if _, err := os.Stat(strings.TrimSuffix(archivePath, ".tar.zst") + ".json"); err != nil {
		t.Fatalf("archive sidecar: %v", err)
	}
}

func readE2ERetentionArchive(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	decompressor, err := zstd.NewReader(file)
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	defer decompressor.Close()
	defer func() { _ = file.Close() }()
	reader := tar.NewReader(decompressor)
	entries := map[string]string{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		entries[strings.TrimSuffix(header.Name, "/")] = string(contents)
	}
}

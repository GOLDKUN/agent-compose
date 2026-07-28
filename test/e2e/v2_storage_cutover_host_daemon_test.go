package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	domain "agent-compose/pkg/model"
	storagesqlite "agent-compose/pkg/storage/sqlite"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
	"agent-compose/proto/agentcompose/v2/agentcomposev2connect"
)

const (
	v2StorageCutoverImageEnv    = "AGENT_COMPOSE_E2E_V2_STORAGE_CUTOVER_IMAGE"
	v2StorageCutoverMigratorEnv = "AGENT_COMPOSE_E2E_MIGRATOR_BINARY"
	v2StorageCutoverEnvValue    = "persisted-through-v2-cutover"
	v2StorageCutoverGlobalValue = "global-through-v2-cutover"
	v2StorageCutoverAgentName   = "cutover-worker"
	v2StorageCutoverTriggerID   = "cutover-manual"
)

func TestE2EV2StorageMigratorDockerCutover(t *testing.T) {
	image := strings.TrimSpace(os.Getenv(v2StorageCutoverImageEnv))
	if image == "" {
		t.Skipf("set %s to a local Docker guest image", v2StorageCutoverImageEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	repoRoot := e2eRepoRoot(t)
	testRoot, err := os.MkdirTemp(repoRoot, ".v2-storage-cutover-e2e-")
	if err != nil {
		t.Fatalf("create Docker-visible cutover E2E root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(testRoot) })
	dockerClient := newE2EDockerClient(t, ctx, image)
	seedV4StandaloneScheduler(t, ctx, filepath.Join(testRoot, "data.db"), image)

	migrator := e2eMigratorBinary(t, repoRoot, testRoot)
	runV2StorageMigrator(t, ctx, migrator, testRoot)
	binary := e2eDaemonBinary(t, ctx, repoRoot, testRoot)
	listenAddress := unusedLoopbackAddress(t)
	baseURL := "http://" + listenAddress
	daemon := startE2EDaemon(t, binary, repoRoot, testRoot, listenAddress, image)
	waitForE2EDaemon(t, ctx, daemon, baseURL)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("cutover daemon log:\n%s", daemon.logs.String())
		}
	})

	httpClient := newE2EHTTPClient()
	t.Cleanup(httpClient.CloseIdleConnections)
	projectClient := agentcomposev2connect.NewProjectServiceClient(httpClient, baseURL)
	sandboxClient := agentcomposev2connect.NewSandboxServiceClient(httpClient, baseURL)
	projectID := migratedLegacyProjectID(t, ctx, projectClient)
	run := runMigratedScheduler(t, ctx, projectClient, projectID)
	assertMigratedSchedulerResult(t, run.GetResultJson())
	sandboxID := migratedRunSandboxID(t, ctx, projectClient, projectID, run.GetRunId(), run.GetSandboxIds())
	removed := false
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if !removed {
			_, _ = sandboxClient.RemoveSandbox(cleanupCtx, connect.NewRequest(&agentcomposev2.RemoveSandboxRequest{SandboxId: sandboxID, Force: true}))
		}
		removeE2EDockerSandboxFallback(t, cleanupCtx, dockerClient, sandboxID)
	})

	sandboxResp, err := sandboxClient.GetSandbox(ctx, connect.NewRequest(&agentcomposev2.GetSandboxRequest{SandboxId: sandboxID}))
	if err != nil {
		t.Fatalf("GetSandbox after cutover: %v", err)
	}
	if sandboxResp.Msg.GetSandbox().GetStatus() != domain.VMStatusStopped || sandboxResp.Msg.GetSandbox().GetProjectId() != projectID {
		t.Fatalf("migrated scheduler sandbox = %#v, want completed stopped sandbox for project %s", sandboxResp.Msg.GetSandbox(), projectID)
	}
	container := inspectE2EDockerSandboxContainer(t, ctx, dockerClient, sandboxID)
	if container.State == nil || container.State.Running {
		t.Fatalf("migrated scheduler Docker sandbox = %#v, want stopped container after one-shot execution", container.State)
	}
	removeResp, err := sandboxClient.RemoveSandbox(ctx, connect.NewRequest(&agentcomposev2.RemoveSandboxRequest{SandboxId: sandboxID, Force: true}))
	if err != nil {
		t.Fatalf("RemoveSandbox after cutover: %v", err)
	}
	if !removeResp.Msg.GetRemoved() || removeResp.Msg.GetSandboxId() != sandboxID {
		t.Fatalf("RemoveSandbox after cutover = %#v, want removed sandbox %s", removeResp.Msg, sandboxID)
	}
	removed = true
	removeE2EDockerSandboxFallback(t, ctx, dockerClient, sandboxID)
	assertE2EDockerSandboxContainerCount(t, ctx, dockerClient, sandboxID, 0)

	httpClient.CloseIdleConnections()
	daemon.stop(t)
	assertE2EDaemonReleased(t, daemon, filepath.Join(testRoot, "agent-compose.sock"), listenAddress)
}

func seedV4StandaloneScheduler(t *testing.T, ctx context.Context, databasePath, image string) {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open v4 seed database: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := storagesqlite.MigrateThrough(ctx, db, 4); err != nil {
		_ = db.Close()
		t.Fatalf("create v4 seed schema: %v", err)
	}
	script := `scheduler.on("cutover.e2e", "cutover-manual", async function cutoverE2E() {
  const result = await scheduler.shell(
    "printf 'CUTOVER_ENV=%s\\nGLOBAL_CUTOVER=%s\\n' \"$CUTOVER_ENV\" \"$GLOBAL_CUTOVER\"",
    { sandboxPolicy: "new", title: "v2 storage cutover e2e" }
  );
  if (!result.success) throw new Error("cutover environment probe failed");
  return { output: result.output || result.stdout || "" };
});`
	envJSON, err := json.Marshal([]map[string]any{{"name": "CUTOVER_ENV", "value": v2StorageCutoverEnvValue}})
	if err != nil {
		_ = db.Close()
		t.Fatalf("encode legacy scheduler environment: %v", err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{query: `INSERT INTO global_env(name,value,secret,updated_at) VALUES('GLOBAL_CUTOVER',?,0,1000)`, args: []any{v2StorageCutoverGlobalValue}},
		{query: `INSERT INTO loader(
			id,name,runtime,script,driver,guest_image,default_agent,sandbox_policy,concurrency_policy,env_json,enabled,created_at,updated_at
		) VALUES('cutover-loader',?,'scheduler',?,'docker',?,'codex','new','skip',?,1,1000,1001)`, args: []any{v2StorageCutoverAgentName, script, image, string(envJSON)}},
		{query: `INSERT INTO loader_trigger(loader_id,trigger_id,kind,topic,enabled,spec_json)
			VALUES('cutover-loader',?,'event','cutover.e2e',1,'{"topic":"cutover.e2e"}')`, args: []any{v2StorageCutoverTriggerID}},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			_ = db.Close()
			t.Fatalf("seed v4 standalone scheduler with %q: %v", statement.query, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v4 seed database: %v", err)
	}
}

func e2eMigratorBinary(t *testing.T, repoRoot, testRoot string) string {
	t.Helper()
	if configured := strings.TrimSpace(os.Getenv(v2StorageCutoverMigratorEnv)); configured != "" {
		binary, err := filepath.Abs(configured)
		if err != nil {
			t.Fatalf("resolve %s: %v", v2StorageCutoverMigratorEnv, err)
		}
		return binary
	}
	binary := filepath.Join(testRoot, "agent-compose-v2-storage-migrator")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/agent-compose-migrate")
	cmd.Dir = repoRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build storage migrator: %v\n%s", err, output)
	}
	return binary
}

func runV2StorageMigrator(t *testing.T, ctx context.Context, binary, dataRoot string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, binary,
		"--source", dataRoot,
		"--target", dataRoot,
		"--runtime-root", dataRoot,
		"--json",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("run V2 storage migrator: %v\nstdout:\n%s\nstderr:\n%s", err, output, stderr.String())
	}
	var report struct {
		Stage         string `json:"stage"`
		SourceVersion int64  `json:"source_version"`
		TargetVersion int64  `json:"target_version"`
		InPlace       bool   `json:"in_place"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode V2 storage migrator report %q: %v", output, err)
	}
	if report.Stage != "complete" || report.SourceVersion != 4 || report.TargetVersion != 7 || !report.InPlace {
		t.Fatalf("V2 storage migrator report = %+v, want in-place v4 to v7 completion", report)
	}
}

func migratedLegacyProjectID(t *testing.T, ctx context.Context, client agentcomposev2connect.ProjectServiceClient) string {
	t.Helper()
	response, err := client.ListProjects(ctx, connect.NewRequest(&agentcomposev2.ListProjectsRequest{Query: "legacy-v1-default", Limit: 100}))
	if err != nil {
		t.Fatalf("ListProjects after cutover: %v", err)
	}
	for _, project := range response.Msg.GetProjects() {
		if project.GetName() == "legacy-v1-default" && project.GetProjectId() != "" {
			return project.GetProjectId()
		}
	}
	t.Fatalf("migrated legacy-v1-default project not found: %#v", response.Msg.GetProjects())
	return ""
}

func runMigratedScheduler(t *testing.T, ctx context.Context, client agentcomposev2connect.ProjectServiceClient, projectID string) *agentcomposev2.SchedulerRun {
	t.Helper()
	response, err := client.RunScheduler(ctx, connect.NewRequest(&agentcomposev2.RunSchedulerRequest{
		Project:     &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: projectID}},
		AgentName:   v2StorageCutoverAgentName,
		TriggerId:   v2StorageCutoverTriggerID,
		PayloadJson: `{"source":"cutover-e2e"}`,
	}))
	if err != nil {
		t.Fatalf("RunScheduler after cutover: %v", err)
	}
	run := response.Msg.GetRun()
	if run.GetStatus() != agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_SUCCEEDED || run.GetRunId() == "" {
		t.Fatalf("migrated scheduler run = %#v, want succeeded run", run)
	}
	list, err := client.ListSchedulerRuns(ctx, connect.NewRequest(&agentcomposev2.ListSchedulerRunsRequest{
		Project:   &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: projectID}},
		AgentName: v2StorageCutoverAgentName,
		Limit:     10,
	}))
	if err != nil {
		t.Fatalf("ListSchedulerRuns after cutover: %v", err)
	}
	for _, stored := range list.Msg.GetRuns() {
		if stored.GetRunId() == run.GetRunId() && stored.GetStatus() == run.GetStatus() {
			return run
		}
	}
	t.Fatalf("completed migrated scheduler run %s not returned by ListSchedulerRuns", run.GetRunId())
	return nil
}

func assertMigratedSchedulerResult(t *testing.T, resultJSON string) {
	t.Helper()
	var result struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("decode migrated scheduler result %q: %v", resultJSON, err)
	}
	for _, want := range []string{
		"CUTOVER_ENV=" + v2StorageCutoverEnvValue,
		"GLOBAL_CUTOVER=" + v2StorageCutoverGlobalValue,
	} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("migrated scheduler output %q does not contain %q", result.Output, want)
		}
	}
}

func migratedRunSandboxID(t *testing.T, ctx context.Context, client agentcomposev2connect.ProjectServiceClient, projectID, runID string, runSandboxIDs []string) string {
	t.Helper()
	linked := make(map[string]struct{}, len(runSandboxIDs))
	for _, sandboxID := range runSandboxIDs {
		if sandboxID = strings.TrimSpace(sandboxID); sandboxID != "" {
			linked[sandboxID] = struct{}{}
		}
	}
	response, err := client.ListProjectSchedulerEvents(ctx, connect.NewRequest(&agentcomposev2.ListProjectSchedulerEventsRequest{
		Project:   &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: projectID}},
		AgentName: v2StorageCutoverAgentName,
		RunId:     runID,
		Limit:     100,
	}))
	if err != nil {
		t.Fatalf("ListProjectSchedulerEvents after cutover: %v", err)
	}
	completed := false
	for _, event := range response.Msg.GetEvents() {
		if sandboxID := strings.TrimSpace(event.GetLinkedSandboxId()); sandboxID != "" {
			linked[sandboxID] = struct{}{}
		}
		if event.GetType() == "scheduler.command.completed" && event.GetLevel() == "info" {
			completed = true
		}
	}
	if !completed || len(linked) != 1 {
		t.Fatalf("migrated scheduler events completed=%t linked_sandboxes=%v events=%d", completed, linked, len(response.Msg.GetEvents()))
	}
	for sandboxID := range linked {
		return sandboxID
	}
	t.Fatalf("migrated scheduler run %s has no linked sandbox", runID)
	return ""
}

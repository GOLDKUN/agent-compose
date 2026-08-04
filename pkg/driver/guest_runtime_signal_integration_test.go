//go:build linux

package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestIntegrationGuestRuntimeSignalRequiresExactRuntimeReadiness(t *testing.T) {
	executionID := uuid.NewString()
	readyFile := filepath.Join(guestRuntimeReadyRoot, executionID)
	if err := os.MkdirAll(filepath.Dir(readyFile), 0o700); err != nil {
		t.Fatalf("create readiness root: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(readyFile) })

	testRoot := t.TempDir()
	nodePath := filepath.Join(testRoot, "node")
	shell, err := os.ReadFile("/bin/sh")
	if err != nil {
		t.Fatalf("read shell executable: %v", err)
	}
	if err := os.WriteFile(nodePath, shell, 0o755); err != nil {
		t.Fatalf("create node-named test process: %v", err)
	}
	startedPath := filepath.Join(testRoot, "started")
	installHandlerPath := filepath.Join(testRoot, "install-handler")
	handledPath := filepath.Join(testRoot, "handled")
	process := exec.Command(nodePath, "-c", testGuestRuntimeProcessScript, "agent-compose-runtime", startedPath, installHandlerPath, handledPath, readyFile)
	controlEnv := GuestRuntimeControlEnv(map[string]string{
		"PATH":                os.Getenv("PATH"),
		executionIDEnv:        "stale-execution-id",
		executionReadyFileEnv: "/tmp/stale-ready-file",
	}, executionID)
	process.Env = make([]string, 0, len(controlEnv))
	for name, value := range controlEnv {
		process.Env = append(process.Env, name+"="+value)
	}
	if err := process.Start(); err != nil {
		t.Fatalf("start node-named test process: %v", err)
	}
	processDone := make(chan error, 1)
	go func() { processDone <- process.Wait() }()
	waited := false
	t.Cleanup(func() {
		if waited {
			return
		}
		_ = process.Process.Kill()
		<-processDone
	})
	waitForGuestRuntimeTestFile(t, startedPath)

	if err := os.WriteFile(readyFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0o600); err != nil {
		t.Fatalf("write stale runtime readiness: %v", err)
	}
	if err := runGuestRuntimeSignalCommand(executionID); !errors.Is(err, ErrGuestRuntimeGone) {
		t.Fatalf("stale runtime readiness error = %v, want ErrGuestRuntimeGone", err)
	}
	if err := os.Remove(readyFile); err != nil {
		t.Fatalf("remove stale runtime readiness: %v", err)
	}
	if err := runGuestRuntimeSignalCommand(executionID); !errors.Is(err, ErrGuestRuntimeNotReady) {
		t.Fatalf("signal before runtime readiness error = %v, want ErrGuestRuntimeNotReady", err)
	}
	select {
	case err := <-processDone:
		waited = true
		t.Fatalf("managed execution exited before installing SIGTERM handler: %v", err)
	default:
	}
	if err := os.WriteFile(installHandlerPath, []byte("install"), 0o600); err != nil {
		t.Fatalf("request SIGTERM handler installation: %v", err)
	}
	waitForGuestRuntimeTestFile(t, readyFile)

	if err := runGuestRuntimeSignalCommand(executionID); err != nil {
		t.Fatalf("signal ready managed execution: %v", err)
	}
	select {
	case err := <-processDone:
		waited = true
		if err != nil {
			t.Fatalf("managed execution exit after SIGTERM: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("managed execution did not handle SIGTERM")
	}
	if _, err := os.Stat(handledPath); err != nil {
		t.Fatalf("SIGTERM handler result: %v", err)
	}
}

func runGuestRuntimeSignalCommand(executionID string) error {
	command, err := guestRuntimeSignalCommand(executionID, RuntimeSignalTerminate)
	if err != nil {
		return err
	}
	err = exec.CommandContext(context.Background(), command[0], command[1:]...).Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return guestRuntimeSignalExitError(exitErr.ExitCode())
	}
	return err
}

func waitForGuestRuntimeTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("wait for %s: %v", filepath.Base(path), err)
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", filepath.Base(path))
		case <-ticker.C:
		}
	}
}

const testGuestRuntimeProcessScript = `
printf started >"$1"
while [ ! -e "$2" ]; do sleep 0.01; done
trap 'printf handled >"$3"; exit 0' TERM
printf '%s' "$$" >"$4"
while :; do sleep 1; done
`

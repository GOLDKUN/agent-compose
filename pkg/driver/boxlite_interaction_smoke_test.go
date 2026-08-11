//go:build linux && cgo && boxlitecgo

package driver

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestBoxliteCommandInteractionSmokeTTYStdinAndResize(t *testing.T) {
	runtimeSmokeEnabled(t, RuntimeDriverBoxlite)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	config := newRuntimeSmokeConfig(t, RuntimeDriverBoxlite)
	runtime, err := newSandboxRuntime(config)
	if err != nil {
		t.Fatalf("newSandboxRuntime() error = %v", err)
	}
	boxliteRuntime := runtime.(*cgoSandboxRuntime)
	sandbox, vmState, proxyState := newRuntimeSmokeSandbox(t, ctx, config, RuntimeDriverBoxlite)
	proxyState.Enabled = false
	info, err := boxliteRuntime.EnsureSandbox(ctx, sandbox, vmState, proxyState)
	if err != nil {
		t.Fatalf("EnsureSandbox() error = %v", err)
	}
	vmState.BoxID = info.BoxID
	cleanupRuntimeSmokeSandbox(t, config, boxliteRuntime, sandbox, vmState)

	interaction, err := boxliteRuntime.OpenInteraction(ctx, sandbox, vmState, RuntimeStartSpec{
		OperationID: "smoke-boxlite-it",
		Kind:        RuntimeOperationCommand,
		AttachStdin: true,
		TTY:         true,
		Rows:        24,
		Cols:        80,
		Command: &RuntimeCommandSpec{
			Command: "sh",
			Args:    []string{"-c", `printf 'ready>'; read value; printf 'received:%s\n' "$value"`},
		},
	})
	if err != nil {
		t.Fatalf("OpenInteraction() error = %v", err)
	}
	if frame, err := interaction.Recv(); err != nil || frame.Type != RuntimeOutputStarted {
		t.Fatalf("started frame = %#v, err=%v", frame, err)
	}
	if err := interaction.Send(RuntimeInputFrame{Type: RuntimeInputResize, Rows: 40, Cols: 120}); err != nil {
		t.Fatalf("Send(resize) error = %v", err)
	}
	if err := interaction.Send(RuntimeInputFrame{Type: RuntimeInputStdin, Data: []byte("hello boxlite\n")}); err != nil {
		t.Fatalf("Send(stdin) error = %v", err)
	}
	if err := interaction.CloseSend(); err != nil {
		t.Fatalf("CloseSend() error = %v", err)
	}

	var stdout strings.Builder
	for {
		frame, err := interaction.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv() error = %v", err)
		}
		switch frame.Type {
		case RuntimeOutputStdout:
			stdout.Write(frame.Data)
		case RuntimeOutputStderr:
			t.Fatalf("TTY output unexpectedly used stderr frame: %#v", frame)
		case RuntimeOutputError:
			t.Fatalf("unexpected error frame: %#v", frame)
		}
	}
	result, err := interaction.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if !result.Success || result.ExitCode != 0 {
		t.Fatalf("Wait() result = %#v", result)
	}
	if got := stdout.String(); !strings.Contains(got, "ready>") || !strings.Contains(got, "received:hello boxlite") {
		t.Fatalf("stdout = %q, want interactive prompt and response", got)
	}
}

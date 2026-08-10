//go:build linux && cgo && boxlitecgo

package driver

import (
	"context"
	"errors"
	"math"
	"reflect"
	"syscall"
	"testing"
)

func TestBoxliteInteractionCapabilities(t *testing.T) {
	runtime := &cgoSandboxRuntime{}
	got := runtime.InteractionCapabilities()
	want := RuntimeInteractionCapabilities{
		NativeExec: true,
		Stdin:      true,
		StdinEOF:   true,
		TTY:        true,
		Resize:     true,
		Signal:     true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InteractionCapabilities() = %#v, want %#v", got, want)
	}
}

func TestValidateBoxliteInteractionSpec(t *testing.T) {
	runtime := &cgoSandboxRuntime{}
	valid := RuntimeStartSpec{
		Kind: RuntimeOperationCommand,
		Command: &RuntimeCommandSpec{
			Command: "sh",
		},
		AttachStdin: true,
		TTY:         true,
		Rows:        24,
		Cols:        80,
	}
	if err := runtime.validateBoxliteInteractionSpec(valid); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}

	tests := []struct {
		name string
		spec RuntimeStartSpec
		want error
	}{
		{name: "agent", spec: RuntimeStartSpec{Kind: RuntimeOperationAgent}, want: ErrRuntimeInteractionUnsupported},
		{name: "missing command", spec: RuntimeStartSpec{Kind: RuntimeOperationCommand}, want: nil},
		{name: "oversized terminal", spec: RuntimeStartSpec{Kind: RuntimeOperationCommand, Command: &RuntimeCommandSpec{Command: "sh"}, Rows: math.MaxUint32}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runtime.validateBoxliteInteractionSpec(tt.spec)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, tt.want)
			}
		})
	}
}

func TestBoxliteRuntimeSignal(t *testing.T) {
	tests := []struct {
		input RuntimeSignal
		want  syscall.Signal
	}{
		{input: RuntimeSignalInterrupt, want: syscall.SIGINT},
		{input: RuntimeSignalTerminate, want: syscall.SIGTERM},
		{input: RuntimeSignalKill, want: syscall.SIGKILL},
	}
	for _, tt := range tests {
		got, err := boxliteRuntimeSignal(tt.input)
		if err != nil {
			t.Fatalf("boxliteRuntimeSignal(%q): %v", tt.input, err)
		}
		if got != int(tt.want) {
			t.Fatalf("boxliteRuntimeSignal(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
	if _, err := boxliteRuntimeSignal("unknown"); err == nil {
		t.Fatal("unknown signal accepted")
	}
}

func TestBoxliteInteractionProjectsTTYOutputToStdout(t *testing.T) {
	interaction := &boxliteCommandInteraction{
		ctx:    context.Background(),
		tty:    true,
		output: make(chan RuntimeOutputFrame, 2),
	}
	interaction.emitChunk(ExecChunk{Text: "terminal", Stream: StdioStderr})
	frame := <-interaction.output
	if frame.Type != RuntimeOutputStdout || string(frame.Data) != "terminal" {
		t.Fatalf("frame = %#v", frame)
	}
}

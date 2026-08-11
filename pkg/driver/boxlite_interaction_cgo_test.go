//go:build linux && cgo && boxlitecgo

package driver

import (
	"context"
	"errors"
	"io"
	"math"
	"reflect"
	"syscall"
	"testing"
	"time"
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
		ctx:         context.Background(),
		tty:         true,
		startedAt:   time.Now(),
		output:      make(chan RuntimeOutputFrame, 2),
		startupDone: make(chan struct{}),
	}
	interaction.startOutput()
	if frame := <-interaction.output; frame.Type != RuntimeOutputStarted {
		t.Fatalf("first frame = %#v, want started", frame)
	}
	interaction.emitChunk(ExecChunk{Text: "terminal", Stream: StdioStderr})
	frame := <-interaction.output
	if frame.Type != RuntimeOutputStdout || string(frame.Data) != "terminal" {
		t.Fatalf("frame = %#v", frame)
	}
}

func TestBoxliteInteractionStagesStartupOutputBehindStarted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interaction := &boxliteCommandInteraction{
		ctx:         ctx,
		startedAt:   time.Now(),
		output:      make(chan RuntimeOutputFrame, 64),
		startupDone: make(chan struct{}),
	}
	for index := 0; index < 128; index++ {
		interaction.emit(RuntimeOutputFrame{Type: RuntimeOutputStdout, Data: []byte{byte(index)}})
	}

	started := make(chan struct{})
	go func() {
		interaction.startOutput()
		close(started)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("startOutput blocked before a receiver was available")
	}

	for index := -1; index < 128; index++ {
		select {
		case frame := <-interaction.output:
			if index == -1 {
				if frame.Type != RuntimeOutputStarted {
					t.Fatalf("first frame = %#v, want started", frame)
				}
				continue
			}
			if frame.Type != RuntimeOutputStdout || len(frame.Data) != 1 || frame.Data[0] != byte(index) {
				t.Fatalf("frame %d = %#v", index, frame)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out receiving frame %d", index)
		}
	}
}

func TestBoxliteInteractionCollectorDoesNotRetainStreamOutput(t *testing.T) {
	var chunks []ExecChunk
	collector := &cgoExecCollector{
		streamOnly: true,
		stream: func(chunk ExecChunk) {
			chunks = append(chunks, chunk)
		},
	}
	collector.writeBytes([]byte("stdout"), StdioStdout)
	collector.writeBytes([]byte("stderr"), StdioStderr)
	collector.finish()

	if len(chunks) != 2 || chunks[0].Text != "stdout" || chunks[1].Text != "stderr" {
		t.Fatalf("chunks = %#v", chunks)
	}
	if collector.output.Len() != 0 || collector.stdout.Len() != 0 || collector.stderr.Len() != 0 {
		t.Fatalf("stream-only collector retained output: output=%d stdout=%d stderr=%d", collector.output.Len(), collector.stdout.Len(), collector.stderr.Len())
	}
}

func TestBoxliteInteractionRejectsNativeInputAfterExecutionRelease(t *testing.T) {
	interaction := &boxliteCommandInteraction{ctx: context.Background(), tty: true}

	if err := interaction.resize(24, 80); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("resize error = %v, want %v", err, io.ErrClosedPipe)
	}
	if err := interaction.signal(RuntimeSignalInterrupt); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("signal error = %v, want %v", err, io.ErrClosedPipe)
	}
}

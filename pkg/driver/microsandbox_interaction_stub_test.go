//go:build !linux || !cgo || !microsandboxcgo

package driver

import (
	"context"
	"errors"
	"testing"
)

func TestMicrosandboxInteractionUnsupportedWithoutCompiledDriver(t *testing.T) {
	runtime := &microsandboxRuntime{}
	_, err := runtime.OpenInteraction(context.Background(), nil, VMState{}, RuntimeStartSpec{
		Kind:        RuntimeOperationCommand,
		AttachStdin: true,
		TTY:         true,
	})
	if !errors.Is(err, ErrRuntimeInteractionUnsupported) {
		t.Fatalf("OpenInteraction() error = %v, want ErrRuntimeInteractionUnsupported", err)
	}
}

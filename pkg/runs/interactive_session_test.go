package runs

import (
	"context"
	"errors"
	"testing"

	driverpkg "agent-compose/pkg/driver"
)

type sessionTestInteraction struct{ closed bool }

func (s *sessionTestInteraction) Send(driverpkg.RuntimeInputFrame) error { return nil }
func (s *sessionTestInteraction) CloseSend() error                       { s.closed = true; return nil }
func (s *sessionTestInteraction) Recv() (driverpkg.RuntimeOutputFrame, error) {
	return driverpkg.RuntimeOutputFrame{}, errors.New("eof")
}
func (s *sessionTestInteraction) Wait() (driverpkg.RuntimeResult, error) {
	return driverpkg.RuntimeResult{}, nil
}

func TestInteractiveSessionManagerAttachAndClose(t *testing.T) {
	m := NewInteractiveSessionManager()
	s, err := m.Create("run-1")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &sessionTestInteraction{}
	if err := s.BindRuntime(runtime); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Runtime(); err != nil || got == nil {
		t.Fatalf("runtime = %v, %v", got, err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	input, release, err := s.AttachInput()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := s.Send(context.Background(), RunAttachInput{Kind: RunAttachInputHumanMessage, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if got := (<-input).Text; got != "hello" {
		t.Fatalf("input = %q", got)
	}
	if _, _, err := s.AttachInput(); !errors.Is(err, ErrInteractiveSessionAttached) {
		t.Fatalf("attach error = %v", err)
	}
	if err := m.Remove("run-1", InteractiveSessionCompleted); err != nil {
		t.Fatal(err)
	}
	if !runtime.closed {
		t.Fatal("session close did not close runtime input")
	}
	if err := s.Send(context.Background(), RunAttachInput{}); !errors.Is(err, ErrInteractiveSessionClosed) {
		t.Fatalf("send error = %v", err)
	}
}

func TestInteractiveSessionManagerAttachMissing(t *testing.T) {
	m := NewInteractiveSessionManager()
	if _, err := m.Attach("missing"); !errors.Is(err, ErrInteractiveSessionNotFound) {
		t.Fatalf("attach error = %v", err)
	}
}

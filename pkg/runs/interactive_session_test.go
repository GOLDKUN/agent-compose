package runs

import (
	"context"
	"errors"
	"testing"
)

func TestInteractiveSessionManagerAttachAndClose(t *testing.T) {
	m := NewInteractiveSessionManager()
	s, err := m.Create("run-1")
	if err != nil {
		t.Fatal(err)
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
	if err := s.Send(context.Background(), RunAttachInput{}); !errors.Is(err, ErrInteractiveSessionClosed) {
		t.Fatalf("send error = %v", err)
	}
}

package runs

import (
	"context"
	"errors"
	"testing"

	driverpkg "agent-compose/pkg/driver"
	domain "agent-compose/pkg/model"
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
	release, err := s.AcquireInput()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := s.Send(context.Background(), RunAttachInput{Kind: RunAttachInputHumanMessage, Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	input, err := s.Receive()()
	if err != nil {
		t.Fatal(err)
	}
	if got := input.Text; got != "hello" {
		t.Fatalf("input = %q", got)
	}
	if _, err := s.AcquireInput(); !errors.Is(err, ErrInteractiveSessionAttached) {
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

func TestInteractiveSessionInputLeaseTracksDetachAndResume(t *testing.T) {
	s := NewInteractiveSession("run-1")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	release, err := s.AcquireInput()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcquireInput(); !errors.Is(err, ErrInteractiveSessionAttached) {
		t.Fatalf("second lease error = %v", err)
	}
	release()
	if s.State() != InteractiveSessionDetached {
		t.Fatalf("state = %q", s.State())
	}
	release, err = s.AcquireInput()
	if err != nil {
		t.Fatal(err)
	}
	if s.State() != InteractiveSessionRunning {
		t.Fatalf("resumed state = %q", s.State())
	}
	release()
}

func TestInteractiveSessionClosesSlowOutputSubscriber(t *testing.T) {
	s := NewInteractiveSession("run-1")
	outputs, unsubscribe, err := s.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	for i := 0; i < 33; i++ {
		s.Publish(RunAttachOutput{})
	}
	for range outputs {
	}
}

func TestControllerAttachesExistingInteractiveSession(t *testing.T) {
	m := NewInteractiveSessionManager()
	s, err := m.Create("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	requests := []RunAttachInput{{Kind: RunAttachInputStart, RunID: "run-1"}, {Kind: RunAttachInputHumanMessage, Text: "continue"}}
	i := 0
	receive := func() (RunAttachInput, error) {
		if i == len(requests) {
			return RunAttachInput{}, errors.New("done")
		}
		request := requests[i]
		i++
		return request, nil
	}
	controller := NewController(ControllerDependencies{InteractiveSessions: m})
	controller.configDB = &fakeControllerStore{runs: map[string]domain.ProjectRunRecord{"run-1": {RunID: "run-1", Status: domain.ProjectRunStatusRunning}}}
	err = controller.RunProjectCommandAttach(context.Background(), receive, func(RunAttachOutput) error { return nil })
	if err == nil || err.Error() != "done" {
		t.Fatalf("attach error = %v", err)
	}
	if got := (<-s.input).Text; got != "continue" {
		t.Fatalf("input = %q", got)
	}
}

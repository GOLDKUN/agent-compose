package runs

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrInteractiveSessionNotFound = errors.New("interactive session not found")
	ErrInteractiveSessionClosed   = errors.New("interactive session closed")
	ErrInteractiveSessionAttached = errors.New("interactive session input already attached")
)

type InteractiveSessionState string

const (
	InteractiveSessionCreated   InteractiveSessionState = "created"
	InteractiveSessionRunning   InteractiveSessionState = "running"
	InteractiveSessionDetached  InteractiveSessionState = "detached"
	InteractiveSessionCompleted InteractiveSessionState = "completed"
	InteractiveSessionCanceled  InteractiveSessionState = "canceled"
)

type InteractiveSession struct {
	RunID string

	mu        sync.Mutex
	state     InteractiveSessionState
	input     chan RunAttachInput
	attached  bool
	closeOnce sync.Once
}

func NewInteractiveSession(runID string) *InteractiveSession {
	return &InteractiveSession{RunID: runID, state: InteractiveSessionCreated, input: make(chan RunAttachInput, 32)}
}

func (s *InteractiveSession) State() InteractiveSessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *InteractiveSession) Start() error { return s.transition(InteractiveSessionRunning) }

func (s *InteractiveSession) transition(next InteractiveSessionState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == InteractiveSessionCompleted || s.state == InteractiveSessionCanceled {
		return ErrInteractiveSessionClosed
	}
	s.state = next
	return nil
}

func (s *InteractiveSession) AttachInput() (<-chan RunAttachInput, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == InteractiveSessionCompleted || s.state == InteractiveSessionCanceled {
		return nil, nil, ErrInteractiveSessionClosed
	}
	if s.attached {
		return nil, nil, ErrInteractiveSessionAttached
	}
	s.attached = true
	return s.input, func() { s.mu.Lock(); s.attached = false; s.mu.Unlock() }, nil
}

func (s *InteractiveSession) Send(ctx context.Context, input RunAttachInput) error {
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	if state == InteractiveSessionCompleted || state == InteractiveSessionCanceled {
		return ErrInteractiveSessionClosed
	}
	select {
	case s.input <- input:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *InteractiveSession) Close(state InteractiveSessionState) {
	s.closeOnce.Do(func() { s.mu.Lock(); s.state = state; s.mu.Unlock(); close(s.input) })
}

type InteractiveSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*InteractiveSession
}

func NewInteractiveSessionManager() *InteractiveSessionManager {
	return &InteractiveSessionManager{sessions: map[string]*InteractiveSession{}}
}

func (m *InteractiveSessionManager) Create(runID string) (*InteractiveSession, error) {
	if runID == "" {
		return nil, fmt.Errorf("run id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[runID]; ok {
		return nil, fmt.Errorf("session for %s already exists", runID)
	}
	s := NewInteractiveSession(runID)
	m.sessions[runID] = s
	return s, nil
}

func (m *InteractiveSessionManager) Get(runID string) (*InteractiveSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[runID]
	if !ok {
		return nil, ErrInteractiveSessionNotFound
	}
	return s, nil
}

func (m *InteractiveSessionManager) Remove(runID string, state InteractiveSessionState) error {
	m.mu.Lock()
	s, ok := m.sessions[runID]
	if ok {
		delete(m.sessions, runID)
	}
	m.mu.Unlock()
	if !ok {
		return ErrInteractiveSessionNotFound
	}
	s.Close(state)
	return nil
}

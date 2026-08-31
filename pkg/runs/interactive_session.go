package runs

import (
	"context"
	"errors"
	"fmt"
	"sync"

	driverpkg "agent-compose/pkg/driver"
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

	mu             sync.Mutex
	state          InteractiveSessionState
	input          chan RunAttachInput
	attached       bool
	closeOnce      sync.Once
	runtime        driverpkg.RuntimeInteraction
	subscribers    map[uint64]chan RunAttachOutput
	nextSubscriber uint64
}

type InteractiveSessionAttachment struct {
	session *InteractiveSession
	input   <-chan RunAttachInput
	release func()
}

func (a *InteractiveSessionAttachment) Input() <-chan RunAttachInput { return a.input }
func (a *InteractiveSessionAttachment) Send(ctx context.Context, input RunAttachInput) error {
	return a.session.Send(ctx, input)
}
func (a *InteractiveSessionAttachment) Close() {
	if a.release != nil {
		a.release()
		a.release = nil
	}
}

// BindRuntime transfers ownership of the runtime interaction to the session.
// It may only be called once, before the session is closed.
func (s *InteractiveSession) BindRuntime(interaction driverpkg.RuntimeInteraction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtime != nil {
		return fmt.Errorf("runtime interaction already bound")
	}
	if s.state == InteractiveSessionCompleted || s.state == InteractiveSessionCanceled {
		return ErrInteractiveSessionClosed
	}
	s.runtime = driverpkg.GuardRuntimeInteractionInput(interaction)
	return nil
}

func (s *InteractiveSession) Runtime() (driverpkg.RuntimeInteraction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtime == nil {
		return nil, fmt.Errorf("runtime interaction is not bound")
	}
	return s.runtime, nil
}

func NewInteractiveSession(runID string) *InteractiveSession {
	return &InteractiveSession{RunID: runID, state: InteractiveSessionCreated, input: make(chan RunAttachInput, 32), subscribers: map[uint64]chan RunAttachOutput{}}
}

func (s *InteractiveSession) Subscribe() (<-chan RunAttachOutput, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == InteractiveSessionCompleted || s.state == InteractiveSessionCanceled {
		return nil, nil, ErrInteractiveSessionClosed
	}
	id := s.nextSubscriber
	s.nextSubscriber++
	ch := make(chan RunAttachOutput, 32)
	s.subscribers[id] = ch
	return ch, func() {
		s.mu.Lock()
		if current, ok := s.subscribers[id]; ok {
			delete(s.subscribers, id)
			close(current)
		}
		s.mu.Unlock()
	}, nil
}

func (s *InteractiveSession) Publish(output RunAttachOutput) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, subscriber := range s.subscribers {
		select {
		case subscriber <- output:
		default:
		}
	}
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

func (s *InteractiveSession) Receive() RunAttachReceiver {
	return func() (RunAttachInput, error) {
		input, ok := <-s.input
		if !ok {
			return RunAttachInput{}, ErrInteractiveSessionClosed
		}
		return input, nil
	}
}

func (s *InteractiveSession) Close(state InteractiveSessionState) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.state = state
		runtime := s.runtime
		for id, subscriber := range s.subscribers {
			delete(s.subscribers, id)
			close(subscriber)
		}
		s.mu.Unlock()
		close(s.input)
		if runtime != nil {
			_ = runtime.CloseSend()
		}
	})
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

func (m *InteractiveSessionManager) Attach(runID string) (*InteractiveSessionAttachment, error) {
	s, err := m.Get(runID)
	if err != nil {
		return nil, err
	}
	input, release, err := s.AttachInput()
	if err != nil {
		return nil, err
	}
	return &InteractiveSessionAttachment{session: s, input: input, release: release}, nil
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

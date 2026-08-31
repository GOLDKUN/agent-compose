package app

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/samber/do/v2"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/runs"
	"agent-compose/pkg/storage/configstore"
)

type RunSupervisor struct {
	root       context.Context
	controller *runs.Controller
	store      *configstore.ConfigStore

	mu     sync.Mutex
	active map[string]*activeRun
	wg     sync.WaitGroup
}

type activeRun struct {
	cancel   context.CancelCauseFunc
	stopOnce sync.Once
	stopping bool
	stopped  bool
	stopErr  error
}

func NewRunSupervisor(di do.Injector) (*RunSupervisor, error) {
	return &RunSupervisor{
		root:       do.MustInvoke[context.Context](di),
		controller: do.MustInvoke[*runs.Controller](di),
		store:      do.MustInvoke[*configstore.ConfigStore](di),
		active:     map[string]*activeRun{},
	}, nil
}

func (s *RunSupervisor) StartRun(ctx context.Context, req runs.RunAgentRequest) (domain.ProjectRunRecord, error) {
	if req.Interactive {
		return s.startInteractiveRun(ctx, req)
	}
	started, err := s.controller.StartProjectRun(ctx, req)
	if err != nil {
		return domain.ProjectRunRecord{}, err
	}
	if runs.StatusIsTerminal(started.Run.Status) {
		return started.Run, nil
	}
	execCtx, cancel := context.WithCancelCause(s.root)
	s.register(started.Run.RunID, cancel)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.unregister(started.Run.RunID)
		_, _, _ = started.Execute(execCtx, nil)
	}()
	return started.Run, nil
}

func (s *RunSupervisor) startInteractiveRun(ctx context.Context, req runs.RunAgentRequest) (domain.ProjectRunRecord, error) {
	execCtx, cancel := context.WithCancelCause(s.root)
	started := make(chan string, 1)
	failed := make(chan error, 1)
	first := true
	receive := func() (runs.RunAttachInput, error) {
		if first {
			first = false
			return runs.RunAttachInput{Kind: runs.RunAttachInputStart, Mode: runs.RunAttachModePrompt, Request: req, DisconnectPolicy: runs.AttachDisconnectDetach}, nil
		}
		<-execCtx.Done()
		return runs.RunAttachInput{}, context.Cause(execCtx)
	}
	go func() {
		var runID string
		err := s.controller.RunProjectCommandAttachRegistered(execCtx, receive, func(runs.RunAttachOutput) error { return nil }, func(id string) {
			runID = id
			s.register(id, cancel)
			s.wg.Add(1)
			started <- id
		})
		if runID != "" {
			s.wg.Done()
			s.unregister(runID)
		} else {
			cancel(nil)
		}
		failed <- err
	}()
	select {
	case runID := <-started:
		return s.store.GetProjectRun(ctx, runID)
	case err := <-failed:
		return domain.ProjectRunRecord{}, err
	case <-ctx.Done():
		return domain.ProjectRunRecord{}, ctx.Err()
	}
}

func (s *RunSupervisor) Run(ctx context.Context, req runs.RunAgentRequest, stream *runs.StreamSink) (domain.ProjectRunRecord, error, error) {
	started, err := s.controller.StartProjectRun(ctx, req)
	if err != nil {
		return domain.ProjectRunRecord{}, nil, err
	}
	if runs.StatusIsTerminal(started.Run.Status) {
		return started.Run, nil, nil
	}
	execCtx, cancel := context.WithCancelCause(ctx)
	s.register(started.Run.RunID, cancel)
	s.wg.Add(1)
	defer s.wg.Done()
	defer s.unregister(started.Run.RunID)
	return started.Execute(execCtx, stream)
}

func (s *RunSupervisor) Attach(ctx context.Context, receive runs.RunAttachReceiver, send runs.RunAttachSender) error {
	first, err := receive()
	if err != nil {
		return err
	}
	replayed := false
	receiveWithFirst := func() (runs.RunAttachInput, error) {
		if !replayed {
			replayed = true
			return first, nil
		}
		return receive()
	}
	parent := ctx
	if first.DisconnectPolicy == runs.AttachDisconnectDetach && first.RunID == "" {
		parent = s.root
	}
	execCtx, cancel := context.WithCancelCause(parent)
	var runID string
	err = s.controller.RunProjectCommandAttachRegistered(execCtx, receiveWithFirst, send, func(startedRunID string) {
		runID = startedRunID
		s.register(runID, cancel)
		s.wg.Add(1)
	})
	if runID != "" {
		s.wg.Done()
		s.unregister(runID)
	} else {
		cancel(nil)
	}
	return err
}

func (s *RunSupervisor) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *RunSupervisor) StopActiveRun(ctx context.Context, runID, reason string) (bool, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return false, nil
	}
	s.mu.Lock()
	active, ok := s.active[runID]
	if ok {
		active.stopping = true
	}
	s.mu.Unlock()
	if !ok {
		return false, nil
	}
	active.stopOnce.Do(func() {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			reason = "stop requested"
		}
		active.cancel(errors.New(reason))
		active.stopped = true
	})
	return active.stopped, active.stopErr
}

func (s *RunSupervisor) register(runID string, cancel context.CancelCauseFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[runID] = &activeRun{cancel: cancel}
}

func (s *RunSupervisor) unregister(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, runID)
}

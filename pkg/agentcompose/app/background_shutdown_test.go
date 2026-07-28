package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/samber/do/v2"

	"agent-compose/pkg/cleanup"
	"agent-compose/pkg/sandboxes"
)

func TestStopBackgroundComponentsCancelsComponentsConcurrently(t *testing.T) {
	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	firstRelease := make(chan struct{})
	secondRelease := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- stopBackgroundComponents(context.Background(), []backgroundComponent{
			{name: "first", shutdown: blockingBackgroundShutdown(firstEntered, firstRelease)},
			{name: "second", shutdown: blockingBackgroundShutdown(secondEntered, secondRelease)},
		})
	}()

	for name, entered := range map[string]<-chan struct{}{"first": firstEntered, "second": secondEntered} {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatalf("%s background component did not begin shutdown", name)
		}
	}
	select {
	case err := <-done:
		t.Fatalf("stop returned before components were released: %v", err)
	default:
	}
	close(firstRelease)
	close(secondRelease)
	if err := <-done; err != nil {
		t.Fatalf("stop returned error: %v", err)
	}
}

func TestStopBackgroundComponentsJoinsSetupAndShutdownErrors(t *testing.T) {
	setupErr := errors.New("resolve recovery")
	shutdownErr := errors.New("cleanup stuck")
	err := stopBackgroundComponents(context.TODO(), []backgroundComponent{{
		name: "cleanup runner",
		shutdown: func(context.Context) error {
			return shutdownErr
		},
	}}, setupErr)
	if !errors.Is(err, setupErr) || !errors.Is(err, shutdownErr) {
		t.Fatalf("stop error = %v, want both setup and shutdown failures", err)
	}
	if !strings.Contains(err.Error(), "stop cleanup runner") {
		t.Fatalf("stop error = %q, want component context", err)
	}
}

func TestStopBackgroundSkipsComponentsThatFailToResolve(t *testing.T) {
	recoveryErr := errors.New("recovery setup failed")
	runnerErr := errors.New("cleanup setup failed")
	di := do.New()
	do.Provide(di, func(do.Injector) (*sandboxes.DeletionRecovery, error) {
		return nil, recoveryErr
	})
	do.Provide(di, func(do.Injector) (*cleanup.Runner, error) {
		return nil, runnerErr
	})

	err := StopBackground(context.Background(), di)
	if !errors.Is(err, recoveryErr) || !errors.Is(err, runnerErr) {
		t.Fatalf("StopBackground error = %v, want both setup failures", err)
	}
	for _, want := range []string{"resolve sandbox deletion recovery", "resolve cleanup runner"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("StopBackground error = %q, want %q", err, want)
		}
	}
}

func blockingBackgroundShutdown(entered chan<- struct{}, release <-chan struct{}) func(context.Context) error {
	return func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

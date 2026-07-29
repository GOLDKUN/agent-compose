package projects

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestProjectLifecycleGatesSerializeSameNameAndReleaseEntries(t *testing.T) {
	var gates projectLifecycleGates
	releaseFirst, err := gates.acquire(context.Background(), "demo")
	if err != nil {
		t.Fatalf("acquire first gate: %v", err)
	}

	acquired := make(chan func(), 1)
	go func() {
		release, acquireErr := gates.acquire(context.Background(), "demo")
		if acquireErr == nil {
			acquired <- release
		}
	}()
	waitForLifecycleGateRefs(t, &gates, "demo", 2)
	select {
	case release := <-acquired:
		release()
		t.Fatal("same-name lifecycle operation acquired before release")
	default:
	}

	releaseFirst()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("same-name lifecycle operation did not acquire after release")
	}
	gates.mu.Lock()
	defer gates.mu.Unlock()
	if len(gates.gates) != 0 {
		t.Fatalf("released lifecycle gates = %#v, want empty", gates.gates)
	}
}

func TestProjectLifecycleGatesHonorCancellationAndAllowDifferentNames(t *testing.T) {
	var gates projectLifecycleGates
	releaseDemo, err := gates.acquire(context.Background(), "demo")
	if err != nil {
		t.Fatalf("acquire demo gate: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gates.acquire(ctx, "demo"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire error = %v, want context canceled", err)
	}
	releaseOther, err := gates.acquire(context.Background(), "other")
	if err != nil {
		t.Fatalf("acquire different-name gate: %v", err)
	}
	releaseOther()
	releaseDemo()
}

func TestProjectLifecycleGateReleaseIsIdempotent(t *testing.T) {
	var gates projectLifecycleGates
	release, err := gates.acquire(context.Background(), "demo")
	if err != nil {
		t.Fatalf("acquire gate: %v", err)
	}

	released := make(chan struct{})
	go func() {
		release()
		release()
		close(released)
	}()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("repeated lifecycle release blocked")
	}

	gates.mu.Lock()
	defer gates.mu.Unlock()
	if len(gates.gates) != 0 {
		t.Fatalf("released lifecycle gates = %#v, want empty", gates.gates)
	}
}

func waitForLifecycleGateRefs(t *testing.T, gates *projectLifecycleGates, name string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		gates.mu.Lock()
		refs := 0
		if gate := gates.gates[name]; gate != nil {
			refs = gate.refs
		}
		gates.mu.Unlock()
		if refs == want {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("lifecycle gate %q did not reach %d references", name, want)
}

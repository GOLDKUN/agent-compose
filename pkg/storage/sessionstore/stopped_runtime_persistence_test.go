package sessionstore

import (
	"context"
	"testing"

	domain "agent-compose/pkg/model"
)

func TestCreateSandboxSnapshotsStoppedRuntimePolicy(t *testing.T) {
	store := newTestStore(t)
	sandbox, err := store.CreateSandboxWithOptions(context.Background(), "policy", "", "docker", "", "", "test", nil, nil, nil, CreateSandboxOptions{
		StoppedRuntimePolicy: domain.StoppedRuntimePolicyRemove,
	})
	if err != nil {
		t.Fatalf("CreateSandboxWithOptions returned error: %v", err)
	}
	loaded, err := store.GetSandbox(context.Background(), sandbox.Summary.ID)
	if err != nil {
		t.Fatalf("GetSandbox returned error: %v", err)
	}
	if loaded.StoppedRuntimePolicy != domain.StoppedRuntimePolicyRemove {
		t.Fatalf("stopped runtime policy = %q, want remove", loaded.StoppedRuntimePolicy)
	}
}

func TestCreateSandboxDefaultsStoppedRuntimePolicyToRetain(t *testing.T) {
	store := newTestStore(t)
	sandbox, err := store.CreateSandbox(context.Background(), "policy", "", "docker", "", "", "test", nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateSandbox returned error: %v", err)
	}
	if sandbox.StoppedRuntimePolicy != domain.StoppedRuntimePolicyRetain {
		t.Fatalf("stopped runtime policy = %q, want retain", sandbox.StoppedRuntimePolicy)
	}
}

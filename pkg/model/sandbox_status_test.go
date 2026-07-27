package model_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	domain "agent-compose/pkg/model"
)

func TestNormalizeSandboxVMStatus(t *testing.T) {
	for _, status := range domain.SupportedSandboxVMStatuses() {
		t.Run(strings.ToLower(status), func(t *testing.T) {
			got, err := domain.NormalizeSandboxVMStatus("  " + strings.ToLower(status) + "  ")
			if err != nil || got != status {
				t.Fatalf("NormalizeSandboxVMStatus() = %q, %v; want %q", got, err, status)
			}
		})
	}

	if got, err := domain.NormalizeSandboxVMStatus("  "); err != nil || got != "" {
		t.Fatalf("empty status = %q, %v; want empty", got, err)
	}
	if _, err := domain.NormalizeSandboxVMStatus("definitely-invalid"); !errors.Is(err, domain.ErrInvalidArgument) ||
		!strings.Contains(err.Error(), `invalid sandbox status "definitely-invalid"`) ||
		!strings.Contains(err.Error(), "pending, running, stopped, failed, or deleting") {
		t.Fatalf("invalid status error = %v", err)
	}
}

func TestNormalizeSandboxVMStatuses(t *testing.T) {
	input := []string{" running ", "", "STOPPED", "running", " failed "}
	got, err := domain.NormalizeSandboxVMStatuses(input)
	if err != nil {
		t.Fatalf("NormalizeSandboxVMStatuses() error = %v", err)
	}
	want := []string{domain.VMStatusRunning, domain.VMStatusStopped, domain.VMStatusFailed}
	if !slices.Equal(got, want) {
		t.Fatalf("normalized statuses = %#v, want %#v", got, want)
	}
	if !slices.Equal(input, []string{" running ", "", "STOPPED", "running", " failed "}) {
		t.Fatalf("NormalizeSandboxVMStatuses mutated input: %#v", input)
	}

	if got, err := domain.NormalizeSandboxVMStatuses([]string{"running", "definitely-invalid"}); err == nil || got != nil || !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("mixed invalid statuses = %#v, %v; want nil invalid-argument error", got, err)
	}

	statuses := domain.SupportedSandboxVMStatuses()
	statuses[0] = "MUTATED"
	if domain.SupportedSandboxVMStatuses()[0] != domain.VMStatusPending {
		t.Fatal("SupportedSandboxVMStatuses exposed shared mutable state")
	}
}

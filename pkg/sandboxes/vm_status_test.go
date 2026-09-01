package sandboxes_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	domain "github.com/chaitin/agent-compose/pkg/model"
	"github.com/chaitin/agent-compose/pkg/sandboxes"
)

func TestNormalizeSandboxVMStatus(t *testing.T) {
	for _, status := range sandboxes.SupportedVMStatuses() {
		t.Run(strings.ToLower(status), func(t *testing.T) {
			got, err := sandboxes.NormalizeVMStatus("  " + strings.ToLower(status) + "  ")
			if err != nil || got != status {
				t.Fatalf("NormalizeVMStatus() = %q, %v; want %q", got, err, status)
			}
		})
	}

	if got, err := sandboxes.NormalizeVMStatus("  "); err != nil || got != "" {
		t.Fatalf("empty status = %q, %v; want empty", got, err)
	}
	if _, err := sandboxes.NormalizeVMStatus("definitely-invalid"); !errors.Is(err, domain.ErrInvalidArgument) ||
		!strings.Contains(err.Error(), `invalid sandbox status "definitely-invalid"`) ||
		!strings.Contains(err.Error(), "pending, running, stopped, failed, or deleting") {
		t.Fatalf("invalid status error = %v", err)
	}
}

func TestNormalizeSandboxVMStatuses(t *testing.T) {
	input := []string{" running ", "", "STOPPED", "running", " failed "}
	got, err := sandboxes.NormalizeVMStatuses(input)
	if err != nil {
		t.Fatalf("NormalizeVMStatuses() error = %v", err)
	}
	want := []string{domain.VMStatusRunning, domain.VMStatusStopped, domain.VMStatusFailed}
	if !slices.Equal(got, want) {
		t.Fatalf("normalized statuses = %#v, want %#v", got, want)
	}
	if !slices.Equal(input, []string{" running ", "", "STOPPED", "running", " failed "}) {
		t.Fatalf("NormalizeSandboxVMStatuses mutated input: %#v", input)
	}

	if got, err := sandboxes.NormalizeVMStatuses([]string{"running", "definitely-invalid"}); err == nil || got != nil || !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("mixed invalid statuses = %#v, %v; want nil invalid-argument error", got, err)
	}

	statuses := sandboxes.SupportedVMStatuses()
	statuses[0] = "MUTATED"
	if sandboxes.SupportedVMStatuses()[0] != domain.VMStatusPending {
		t.Fatal("SupportedSandboxVMStatuses exposed shared mutable state")
	}
}

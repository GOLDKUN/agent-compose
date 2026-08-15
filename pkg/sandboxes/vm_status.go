package sandboxes

import (
	"fmt"
	"strings"

	domain "agent-compose/pkg/model"
)

var supportedVMStatuses = [...]string{
	domain.VMStatusPending,
	domain.VMStatusRunning,
	domain.VMStatusStopped,
	domain.VMStatusFailed,
	domain.VMStatusDeleting,
}

// SupportedVMStatuses returns the canonical sandbox VM statuses in lifecycle
// order. The returned slice is owned by the caller.
func SupportedVMStatuses() []string {
	return append([]string(nil), supportedVMStatuses[:]...)
}

// NormalizeVMStatus canonicalizes one optional sandbox VM status.
func NormalizeVMStatus(value string) (string, error) {
	status := strings.ToUpper(strings.TrimSpace(value))
	if status == "" {
		return "", nil
	}
	for _, supported := range supportedVMStatuses {
		if status == supported {
			return supported, nil
		}
	}
	return "", domain.ClassifyError(
		domain.ErrInvalidArgument,
		fmt.Sprintf("invalid sandbox status %q: expected %s", strings.TrimSpace(value), vmStatusExpectation()),
		nil,
	)
}

// NormalizeVMStatuses canonicalizes, removes empty entries, and de-duplicates a
// sandbox VM status filter without mutating the input.
func NormalizeVMStatuses(values []string) ([]string, error) {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		status, err := NormalizeVMStatus(value)
		if err != nil {
			return nil, err
		}
		if status == "" {
			continue
		}
		if _, ok := seen[status]; ok {
			continue
		}
		seen[status] = struct{}{}
		normalized = append(normalized, status)
	}
	return normalized, nil
}

func vmStatusExpectation() string {
	names := make([]string, 0, len(supportedVMStatuses))
	for _, status := range supportedVMStatuses {
		names = append(names, strings.ToLower(status))
	}
	return strings.Join(names[:len(names)-1], ", ") + ", or " + names[len(names)-1]
}

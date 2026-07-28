package model

import (
	"fmt"
	"strings"
)

var supportedSandboxVMStatuses = [...]string{
	VMStatusPending,
	VMStatusRunning,
	VMStatusStopped,
	VMStatusFailed,
	VMStatusDeleting,
}

// SupportedSandboxVMStatuses returns the canonical sandbox VM statuses in
// lifecycle order. The returned slice is owned by the caller.
func SupportedSandboxVMStatuses() []string {
	return append([]string(nil), supportedSandboxVMStatuses[:]...)
}

// NormalizeSandboxVMStatus canonicalizes one optional sandbox VM status.
func NormalizeSandboxVMStatus(value string) (string, error) {
	status := strings.ToUpper(strings.TrimSpace(value))
	if status == "" {
		return "", nil
	}
	for _, supported := range supportedSandboxVMStatuses {
		if status == supported {
			return supported, nil
		}
	}
	return "", ClassifyError(
		ErrInvalidArgument,
		fmt.Sprintf("invalid sandbox status %q: expected %s", strings.TrimSpace(value), sandboxVMStatusExpectation()),
		nil,
	)
}

// NormalizeSandboxVMStatuses canonicalizes, removes empty entries, and
// de-duplicates a sandbox VM status filter without mutating the input.
func NormalizeSandboxVMStatuses(values []string) ([]string, error) {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		status, err := NormalizeSandboxVMStatus(value)
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

func sandboxVMStatusExpectation() string {
	names := make([]string, 0, len(supportedSandboxVMStatuses))
	for _, status := range supportedSandboxVMStatuses {
		names = append(names, strings.ToLower(status))
	}
	return strings.Join(names[:len(names)-1], ", ") + ", or " + names[len(names)-1]
}

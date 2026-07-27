package schedulers

import (
	"strings"

	domain "agent-compose/pkg/model"
)

const retiringSchedulerBindingConfigPrefix = "retiring:"

// SchedulerBindingsMatch reports whether two bindings identify the same sticky
// sandbox state. Persistence timestamps are deliberately excluded because
// they do not participate in compare-and-swap ownership.
func SchedulerBindingsMatch(current, expected domain.SchedulerBinding) bool {
	return strings.TrimSpace(current.SchedulerID) == strings.TrimSpace(expected.SchedulerID) &&
		strings.TrimSpace(current.TriggerID) == strings.TrimSpace(expected.TriggerID) &&
		strings.TrimSpace(current.SandboxID) == strings.TrimSpace(expected.SandboxID) &&
		strings.TrimSpace(current.SandboxConfigHash) == strings.TrimSpace(expected.SandboxConfigHash)
}

// AdoptLegacySchedulerBindingConfigHash returns a replacement that records the
// desired configuration on a binding created before configuration hashes were
// persisted. The caller must install the replacement with compare-and-swap so
// concurrent requests cannot adopt different configurations.
func AdoptLegacySchedulerBindingConfigHash(binding domain.SchedulerBinding, desiredConfigHash string) (domain.SchedulerBinding, bool) {
	desiredConfigHash = strings.TrimSpace(desiredConfigHash)
	if strings.TrimSpace(binding.SandboxConfigHash) != "" || desiredConfigHash == "" {
		return binding, false
	}
	binding.SandboxConfigHash = desiredConfigHash
	return binding, true
}

// RetiringSchedulerBinding returns a compare-and-swap replacement that makes an
// existing sticky sandbox unavailable for reuse before its runtime is stopped.
// The sandbox ID is retained so another request can finish the retirement if
// the request that claimed it exits early.
func RetiringSchedulerBinding(binding domain.SchedulerBinding, desiredConfigHash string) domain.SchedulerBinding {
	binding.SandboxConfigHash = retiringSchedulerBindingConfigPrefix + strings.TrimSpace(desiredConfigHash)
	return binding
}

// RetiringSchedulerBindingConfigHash reports the configuration that a sticky
// binding retirement is preparing to install.
func RetiringSchedulerBindingConfigHash(binding domain.SchedulerBinding) (string, bool) {
	hash, found := strings.CutPrefix(strings.TrimSpace(binding.SandboxConfigHash), retiringSchedulerBindingConfigPrefix)
	if !found {
		return "", false
	}
	return strings.TrimSpace(hash), true
}

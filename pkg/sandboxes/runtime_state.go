package sandboxes

import domain "github.com/chaitin/agent-compose/pkg/model"

func runtimeStopIsCurrent(vmState domain.VMState) bool {
	if vmState.StoppedAt.IsZero() {
		return false
	}
	return (vmState.StartedAt.IsZero() || !vmState.StoppedAt.Before(vmState.StartedAt)) &&
		(vmState.StartAttemptedAt.IsZero() || !vmState.StoppedAt.Before(vmState.StartAttemptedAt))
}

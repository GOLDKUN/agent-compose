package api

import (
	"fmt"
	"strings"

	agentcomposev2 "github.com/chaitin/agent-compose/proto/agentcompose/v2"
)

func sandboxStatusToProto(value string) agentcomposev2.SandboxStatus {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pending":
		return agentcomposev2.SandboxStatus_SANDBOX_STATUS_PENDING
	case "running":
		return agentcomposev2.SandboxStatus_SANDBOX_STATUS_RUNNING
	case "stopped":
		return agentcomposev2.SandboxStatus_SANDBOX_STATUS_STOPPED
	case "failed":
		return agentcomposev2.SandboxStatus_SANDBOX_STATUS_FAILED
	case "deleting":
		return agentcomposev2.SandboxStatus_SANDBOX_STATUS_DELETING
	default:
		return agentcomposev2.SandboxStatus_SANDBOX_STATUS_UNSPECIFIED
	}
}

func sandboxStatusFromProto(value agentcomposev2.SandboxStatus) (string, error) {
	switch value {
	case agentcomposev2.SandboxStatus_SANDBOX_STATUS_PENDING:
		return "pending", nil
	case agentcomposev2.SandboxStatus_SANDBOX_STATUS_RUNNING:
		return "running", nil
	case agentcomposev2.SandboxStatus_SANDBOX_STATUS_STOPPED:
		return "stopped", nil
	case agentcomposev2.SandboxStatus_SANDBOX_STATUS_FAILED:
		return "failed", nil
	case agentcomposev2.SandboxStatus_SANDBOX_STATUS_DELETING:
		return "deleting", nil
	default:
		return "", fmt.Errorf("unsupported sandbox status %d", value)
	}
}

func sandboxStatusesFromProto(values []agentcomposev2.SandboxStatus) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		status, err := sandboxStatusFromProto(value)
		if err != nil {
			return nil, err
		}
		result = append(result, status)
	}
	return result, nil
}

func reclamationStateToProto(value string) agentcomposev2.WorkspaceReclamationState {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "reclaiming":
		return agentcomposev2.WorkspaceReclamationState_WORKSPACE_RECLAMATION_STATE_RECLAIMING
	case "reclaimed":
		return agentcomposev2.WorkspaceReclamationState_WORKSPACE_RECLAMATION_STATE_RECLAIMED
	default:
		return agentcomposev2.WorkspaceReclamationState_WORKSPACE_RECLAMATION_STATE_UNSPECIFIED
	}
}

func schedulerSandboxPolicyToProto(value string) agentcomposev2.SchedulerSandboxPolicy {
	switch value {
	case "sticky":
		return agentcomposev2.SchedulerSandboxPolicy_SCHEDULER_SANDBOX_POLICY_STICKY
	case "new":
		return agentcomposev2.SchedulerSandboxPolicy_SCHEDULER_SANDBOX_POLICY_NEW
	default:
		return agentcomposev2.SchedulerSandboxPolicy_SCHEDULER_SANDBOX_POLICY_UNSPECIFIED
	}
}

func schedulerSandboxPolicyText(value agentcomposev2.SchedulerSandboxPolicy) string {
	switch value {
	case agentcomposev2.SchedulerSandboxPolicy_SCHEDULER_SANDBOX_POLICY_STICKY:
		return "sticky"
	case agentcomposev2.SchedulerSandboxPolicy_SCHEDULER_SANDBOX_POLICY_NEW:
		return "new"
	default:
		return ""
	}
}

func schedulerConcurrencyPolicyToProto(value string) agentcomposev2.SchedulerConcurrencyPolicy {
	switch value {
	case "skip":
		return agentcomposev2.SchedulerConcurrencyPolicy_SCHEDULER_CONCURRENCY_POLICY_SKIP
	case "parallel":
		return agentcomposev2.SchedulerConcurrencyPolicy_SCHEDULER_CONCURRENCY_POLICY_PARALLEL
	default:
		return agentcomposev2.SchedulerConcurrencyPolicy_SCHEDULER_CONCURRENCY_POLICY_UNSPECIFIED
	}
}

func schedulerConcurrencyPolicyText(value agentcomposev2.SchedulerConcurrencyPolicy) string {
	switch value {
	case agentcomposev2.SchedulerConcurrencyPolicy_SCHEDULER_CONCURRENCY_POLICY_SKIP:
		return "skip"
	case agentcomposev2.SchedulerConcurrencyPolicy_SCHEDULER_CONCURRENCY_POLICY_PARALLEL:
		return "parallel"
	default:
		return ""
	}
}

func triggerKindToProto(value string) agentcomposev2.TriggerKind {
	switch value {
	case "cron":
		return agentcomposev2.TriggerKind_TRIGGER_KIND_CRON
	case "interval":
		return agentcomposev2.TriggerKind_TRIGGER_KIND_INTERVAL
	case "timeout":
		return agentcomposev2.TriggerKind_TRIGGER_KIND_TIMEOUT
	case "event":
		return agentcomposev2.TriggerKind_TRIGGER_KIND_EVENT
	default:
		return agentcomposev2.TriggerKind_TRIGGER_KIND_UNSPECIFIED
	}
}

func triggerKindText(value agentcomposev2.TriggerKind) string {
	switch value {
	case agentcomposev2.TriggerKind_TRIGGER_KIND_CRON:
		return "cron"
	case agentcomposev2.TriggerKind_TRIGGER_KIND_INTERVAL:
		return "interval"
	case agentcomposev2.TriggerKind_TRIGGER_KIND_TIMEOUT:
		return "timeout"
	case agentcomposev2.TriggerKind_TRIGGER_KIND_EVENT:
		return "event"
	default:
		return ""
	}
}

func volumeMountTypeToProto(value string) agentcomposev2.VolumeMountType {
	switch value {
	case "volume":
		return agentcomposev2.VolumeMountType_VOLUME_MOUNT_TYPE_VOLUME
	case "bind":
		return agentcomposev2.VolumeMountType_VOLUME_MOUNT_TYPE_BIND
	default:
		return agentcomposev2.VolumeMountType_VOLUME_MOUNT_TYPE_UNSPECIFIED
	}
}

// VolumeMountTypeText maps the transport enum to the compose representation.
func VolumeMountTypeText(value agentcomposev2.VolumeMountType) string {
	switch value {
	case agentcomposev2.VolumeMountType_VOLUME_MOUNT_TYPE_VOLUME:
		return "volume"
	case agentcomposev2.VolumeMountType_VOLUME_MOUNT_TYPE_BIND:
		return "bind"
	default:
		return ""
	}
}

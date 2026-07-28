package main

import agentcomposev2 "agent-compose/proto/agentcompose/v2"

func sandboxStatusText(value agentcomposev2.SandboxStatus) string {
	switch value {
	case agentcomposev2.SandboxStatus_SANDBOX_STATUS_PENDING:
		return "pending"
	case agentcomposev2.SandboxStatus_SANDBOX_STATUS_RUNNING:
		return "running"
	case agentcomposev2.SandboxStatus_SANDBOX_STATUS_STOPPED:
		return "stopped"
	case agentcomposev2.SandboxStatus_SANDBOX_STATUS_FAILED:
		return "failed"
	case agentcomposev2.SandboxStatus_SANDBOX_STATUS_DELETING:
		return "deleting"
	default:
		return "unknown"
	}
}

func sandboxStatusFromText(value string) agentcomposev2.SandboxStatus {
	switch value {
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

func sandboxStatusesFromText(values []string) []agentcomposev2.SandboxStatus {
	result := make([]agentcomposev2.SandboxStatus, 0, len(values))
	for _, value := range values {
		result = append(result, sandboxStatusFromText(value))
	}
	return result
}

func workspaceReclamationStateText(value agentcomposev2.WorkspaceReclamationState) string {
	switch value {
	case agentcomposev2.WorkspaceReclamationState_WORKSPACE_RECLAMATION_STATE_RECLAIMING:
		return "reclaiming"
	case agentcomposev2.WorkspaceReclamationState_WORKSPACE_RECLAMATION_STATE_RECLAIMED:
		return "reclaimed"
	default:
		return ""
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

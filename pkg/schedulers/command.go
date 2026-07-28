package schedulers

import (
	"fmt"
	"strings"

	domain "agent-compose/pkg/model"
)

func ValidateCommandRequest(request domain.SchedulerCommandRequest) error {
	switch strings.ToLower(strings.TrimSpace(request.Mode)) {
	case "exec":
		if strings.TrimSpace(request.Command) == "" {
			return fmt.Errorf("command is required")
		}
	case "shell":
		if strings.TrimSpace(request.Script) == "" {
			return fmt.Errorf("script is required")
		}
	default:
		return fmt.Errorf("scheduler command mode must be exec or shell")
	}
	return nil
}

func CommandCellSource(request domain.SchedulerCommandRequest) string {
	if strings.EqualFold(strings.TrimSpace(request.Mode), "shell") {
		return request.Script
	}
	items := append([]string{request.Command}, request.Args...)
	return strings.Join(items, " ")
}

func CommandRequestRequiresCleanup(scheduler domain.Scheduler, request domain.SchedulerCommandRequest) bool {
	effectivePolicy := domain.NormalizeSchedulerSandboxPolicy(scheduler.Summary.SandboxPolicy)
	if strings.TrimSpace(domain.SchedulerCommandSandboxPolicy(request)) != "" {
		effectivePolicy = domain.NormalizeSchedulerSandboxPolicy(domain.SchedulerCommandSandboxPolicy(request))
	}
	return effectivePolicy == domain.SchedulerSandboxPolicyNew || CommandRequestOverridesSandbox(request)
}

func CommandRequestOverridesSandbox(request domain.SchedulerCommandRequest) bool {
	return strings.TrimSpace(request.Driver) != "" ||
		strings.TrimSpace(request.GuestImage) != "" ||
		strings.TrimSpace(request.WorkspaceID) != "" ||
		len(domain.NormalizeEnvItems(domain.SchedulerCommandSandboxEnv(request))) > 0 ||
		len(request.Volumes) > 0
}

package schedulers

import (
	"fmt"
	"strings"

	domain "github.com/chaitin/agent-compose/pkg/model"
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
	effectivePolicy := NormalizeSandboxPolicy(scheduler.Summary.SandboxPolicy)
	if strings.TrimSpace(CommandSandboxPolicy(request)) != "" {
		effectivePolicy = NormalizeSandboxPolicy(CommandSandboxPolicy(request))
	}
	return effectivePolicy == domain.SchedulerSandboxPolicyNew || CommandRequestOverridesSandbox(request)
}

func CommandRequestOverridesSandbox(request domain.SchedulerCommandRequest) bool {
	return strings.TrimSpace(request.Driver) != "" ||
		strings.TrimSpace(request.GuestImage) != "" ||
		strings.TrimSpace(request.WorkspaceID) != "" ||
		len(domain.NormalizeEnvItems(CommandSandboxEnv(request))) > 0 ||
		len(request.Volumes) > 0
}

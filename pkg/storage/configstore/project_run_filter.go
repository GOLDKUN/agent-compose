package configstore

import (
	"strings"

	"agent-compose/pkg/model"
	"agent-compose/internal/projects"
	"agent-compose/pkg/runs"
)

func projectRunFilter(options model.ProjectRunListOptions) ([]string, []any) {
	where := make([]string, 0, 9)
	args := make([]any, 0, 8)
	if projectID := strings.TrimSpace(options.ProjectID); projectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, projectID)
	}
	if agentName := strings.TrimSpace(options.AgentName); agentName != "" {
		where = append(where, "agent_name = ?")
		args = append(args, agentName)
	}
	if sandboxID := strings.TrimSpace(options.SandboxID); sandboxID != "" {
		where = append(where, "sandbox_id = ?")
		args = append(args, sandboxID)
	}
	if schedulerID := strings.TrimSpace(options.SchedulerID); schedulerID != "" {
		where = append(where, "scheduler_id = ?")
		args = append(args, schedulerID)
	}
	if schedulerRunID := strings.TrimSpace(options.SchedulerRunID); schedulerRunID != "" {
		where = append(where, "scheduler_run_id = ?")
		args = append(args, schedulerRunID)
	}
	if status := strings.TrimSpace(options.Status); status != "" {
		where = append(where, "status = ?")
		args = append(args, projects.NormalizeRunStatus(status))
	}
	if source := strings.TrimSpace(options.Source); source != "" {
		where = append(where, "source = ?")
		args = append(args, runs.NormalizeSource(source))
	}
	if options.StartedFrom != nil || options.StartedTo != nil {
		where = append(where, "started_at > 0")
	}
	if options.StartedFrom != nil {
		where = append(where, "started_at >= ?")
		args = append(args, options.StartedFrom.UnixMilli())
	}
	if options.StartedTo != nil {
		where = append(where, "started_at <= ?")
		args = append(args, options.StartedTo.UnixMilli())
	}
	return where, args
}

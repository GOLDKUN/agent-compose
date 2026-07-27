package main

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"

	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

const schedulerRunLookupBatchSize = 500

func latestSchedulerRunsBySandbox(ctx context.Context, clients cliServiceClients, project *agentcomposev2.Project, sessions []*agentcomposev2.Sandbox) (map[string]composeSchedulerRunItem, error) {
	projectID := strings.TrimSpace(project.GetSummary().GetProjectId())
	targetSandboxIDs := make([]string, 0, len(sessions))
	for _, session := range sessions {
		tags := sessionTagsMap(session.GetTags())
		if tags["origin"] == "scheduler" && tags["project_id"] == projectID && tags["agent"] != "" {
			targetSandboxIDs = appendSchedulerSandboxID(targetSandboxIDs, session.GetSandboxId())
			continue
		}
		if legacySchedulerAgentForProject(tags, project) != "" {
			targetSandboxIDs = appendSchedulerSandboxID(targetSandboxIDs, session.GetSandboxId())
		}
	}
	result := make(map[string]composeSchedulerRunItem)
	for start := 0; start < len(targetSandboxIDs); start += schedulerRunLookupBatchSize {
		end := min(start+schedulerRunLookupBatchSize, len(targetSandboxIDs))
		response, err := clients.project.BatchGetLatestSchedulerRuns(ctx, connect.NewRequest(&agentcomposev2.BatchGetLatestSchedulerRunsRequest{
			Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: projectID}}, SandboxIds: targetSandboxIDs[start:end],
		}))
		if err != nil {
			return nil, commandExitErrorForConnect(err)
		}
		for _, lookup := range response.Msg.GetResults() {
			sandboxID := strings.TrimSpace(lookup.GetSandboxId())
			run := lookup.GetRun()
			if sandboxID == "" || run == nil {
				continue
			}
			result[sandboxID] = schedulerRunPSItem(run)
		}
	}
	return result, nil
}

func appendSchedulerSandboxID(values []string, sandboxID string) []string {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return values
	}
	return append(values, sandboxID)
}

func schedulerRunPSItem(run *agentcomposev2.SchedulerRun) composeSchedulerRunItem {
	return composeSchedulerRunItem{
		RunID: run.GetRunId(), AgentName: run.GetAgentName(),
		StartedAt: formatProtoTimestamp(run.GetStartedAt()), CompletedAt: formatProtoTimestamp(run.GetCompletedAt()),
	}
}

func legacySchedulerSandboxBelongsToProject(tags map[string]string, project *agentcomposev2.Project) bool {
	return legacySchedulerAgentForProject(tags, project) != ""
}

func legacySchedulerAgentForProject(tags map[string]string, project *agentcomposev2.Project) string {
	if tags["project_id"] != "" || tags["origin"] != "loader" {
		return ""
	}
	loaderID := strings.TrimSpace(tags["loader_id"])
	projectID := strings.TrimSpace(project.GetSummary().GetProjectId())
	if loaderID == "" || projectID == "" {
		return ""
	}
	for _, scheduler := range project.GetSchedulers() {
		managedLoaderID, err := domain.StableManagedLoaderID(projectID, scheduler.GetAgentName(), "")
		if err == nil && loaderID == managedLoaderID {
			return strings.TrimSpace(scheduler.GetAgentName())
		}
	}
	return ""
}

func schedulerRunIsNewer(schedulerRun composeSchedulerRunItem, projectRun *agentcomposev2.RunSummary) bool {
	if strings.TrimSpace(schedulerRun.RunID) == "" {
		return false
	}
	if projectRun == nil {
		return true
	}
	return runTimestampAfter(schedulerRunSortTime(schedulerRun), projectRunAssociationSortTime(projectRun))
}

func schedulerRunSortTime(run composeSchedulerRunItem) string {
	return firstNonEmptyString(run.CompletedAt, run.StartedAt)
}

func projectRunAssociationSortTime(run *agentcomposev2.RunSummary) string {
	return firstNonEmptyString(formatProtoTimestamp(run.GetUpdatedAt()), formatProtoTimestamp(run.GetCompletedAt()), formatProtoTimestamp(run.GetStartedAt()), formatProtoTimestamp(run.GetCreatedAt()))
}

func runTimestampAfter(candidate, current string) bool {
	candidateTime, candidateErr := time.Parse(time.RFC3339Nano, candidate)
	currentTime, currentErr := time.Parse(time.RFC3339Nano, current)
	if candidateErr == nil && currentErr == nil {
		return candidateTime.After(currentTime)
	}
	return candidate > current
}

package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	domain "agent-compose/pkg/model"
)

type schedulerRunPageCursor struct {
	ProjectID       string    `json:"project_id"`
	ProjectRevision int64     `json:"project_revision"`
	AgentName       string    `json:"agent_name,omitempty"`
	TriggerID       string    `json:"trigger_id,omitempty"`
	Status          string    `json:"status,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	SchedulerID     string    `json:"scheduler_id"`
	RunID           string    `json:"run_id"`
}

func encodeSchedulerRunCursor(projectID string, projectRevision int64, agentName, triggerID, status string, run domain.SchedulerRunSummary) string {
	payload, err := json.Marshal(schedulerRunPageCursor{
		ProjectID:       strings.TrimSpace(projectID),
		ProjectRevision: projectRevision,
		AgentName:       strings.TrimSpace(agentName),
		TriggerID:       strings.TrimSpace(triggerID),
		Status:          strings.TrimSpace(status),
		StartedAt:       run.StartedAt.UTC(),
		SchedulerID:     strings.TrimSpace(run.SchedulerID),
		RunID:           strings.TrimSpace(run.ID),
	})
	if err != nil {
		// An unencodable timestamp cannot produce a resumable page cursor; an
		// empty cursor makes the caller restart paging from the beginning.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

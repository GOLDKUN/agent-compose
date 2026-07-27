package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	domain "agent-compose/pkg/model"
)

type projectSchedulerEventCursor struct {
	ProjectID       string    `json:"project_id"`
	ProjectRevision int64     `json:"project_revision"`
	AgentName       string    `json:"agent_name,omitempty"`
	TriggerID       string    `json:"trigger_id,omitempty"`
	RunID           string    `json:"run_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	LoaderID        string    `json:"loader_id"`
	EventID         string    `json:"event_id"`
}

func encodeProjectSchedulerEventCursor(projectID string, projectRevision int64, agentName, triggerID, runID string, event domain.LoaderEvent) string {
	payload, _ := json.Marshal(projectSchedulerEventCursor{
		ProjectID:       strings.TrimSpace(projectID),
		ProjectRevision: projectRevision,
		AgentName:       strings.TrimSpace(agentName),
		TriggerID:       strings.TrimSpace(triggerID),
		RunID:           strings.TrimSpace(runID),
		CreatedAt:       event.CreatedAt.UTC(),
		LoaderID:        strings.TrimSpace(event.LoaderID),
		EventID:         strings.TrimSpace(event.ID),
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

type projectSchedulerEventCursor struct {
	ProjectID       string    `json:"project_id"`
	ProjectRevision int64     `json:"project_revision"`
	AgentName       string    `json:"agent_name,omitempty"`
	TriggerID       string    `json:"trigger_id,omitempty"`
	RunID           string    `json:"run_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	SchedulerID     string    `json:"scheduler_id"`
	EventID         string    `json:"event_id"`
}

// projectSchedulerEventCursorRequest identifies the stream selection
// encodeProjectSchedulerEventCursor encodes alongside the event to resume
// from.
type projectSchedulerEventCursorRequest struct {
	ProjectID       string
	ProjectRevision int64
	AgentName       string
	TriggerID       string
	RunID           string
	Event           domain.SchedulerEvent
}

func encodeProjectSchedulerEventCursor(req projectSchedulerEventCursorRequest) string {
	payload, err := json.Marshal(projectSchedulerEventCursor{
		ProjectID:       strings.TrimSpace(req.ProjectID),
		ProjectRevision: req.ProjectRevision,
		AgentName:       strings.TrimSpace(req.AgentName),
		TriggerID:       strings.TrimSpace(req.TriggerID),
		RunID:           strings.TrimSpace(req.RunID),
		CreatedAt:       req.Event.CreatedAt.UTC(),
		SchedulerID:     strings.TrimSpace(req.Event.SchedulerID),
		EventID:         strings.TrimSpace(req.Event.ID),
	})
	if err != nil {
		// An unencodable timestamp cannot produce a resumable checkpoint; an
		// empty cursor makes the caller restart the stream rather than resume
		// from a corrupt position.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	domain "github.com/chaitin/agent-compose/pkg/model"
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

// schedulerRunCursorRequest identifies the run-page selection
// encodeSchedulerRunCursor encodes alongside the run to resume from.
type schedulerRunCursorRequest struct {
	ProjectID       string
	ProjectRevision int64
	AgentName       string
	TriggerID       string
	Status          string
	Run             domain.SchedulerRunSummary
}

func encodeSchedulerRunCursor(req schedulerRunCursorRequest) string {
	payload, err := json.Marshal(schedulerRunPageCursor{
		ProjectID:       strings.TrimSpace(req.ProjectID),
		ProjectRevision: req.ProjectRevision,
		AgentName:       strings.TrimSpace(req.AgentName),
		TriggerID:       strings.TrimSpace(req.TriggerID),
		Status:          strings.TrimSpace(req.Status),
		StartedAt:       req.Run.StartedAt.UTC(),
		SchedulerID:     strings.TrimSpace(req.Run.SchedulerID),
		RunID:           strings.TrimSpace(req.Run.ID),
	})
	if err != nil {
		// An unencodable timestamp cannot produce a resumable page cursor; an
		// empty cursor makes the caller restart paging from the beginning.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

package schedulers

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	domain "agent-compose/pkg/model"
)

func ScanSchedulerTrigger(scan func(dest ...any) error) (domain.SchedulerTrigger, error) {
	var item domain.SchedulerTrigger
	var enabled int
	var autoID int
	var nextFireAtRaw any
	var lastFiredAtRaw any
	if err := scan(&item.SchedulerID, &item.ID, &item.Kind, &item.Topic, &item.IntervalMs, &enabled, &autoID, &item.SpecJSON, &nextFireAtRaw, &lastFiredAtRaw); err != nil {
		return domain.SchedulerTrigger{}, fmt.Errorf("scan scheduler trigger: %w", err)
	}
	item.Enabled = enabled != 0
	item.AutoID = autoID != 0
	item.NextFireAt = parseStoredSchedulerTriggerTime(nextFireAtRaw)
	item.LastFiredAt = parseStoredSchedulerTriggerTime(lastFiredAtRaw)
	return item, nil
}

func ScanSchedulerRun(scan func(dest ...any) error) (domain.SchedulerRunSummary, error) {
	var item domain.SchedulerRunSummary
	var startedAtRaw any
	var completedAtRaw any
	if err := scan(&item.SchedulerID, &item.ID, &item.TriggerID, &item.TriggerKind, &item.TriggerSource, &item.Status, &startedAtRaw, &completedAtRaw, &item.DurationMs, &item.Error, &item.ResultJSON, &item.PayloadJSON, &item.SourceScriptHash, &item.ArtifactsDir); err != nil {
		return domain.SchedulerRunSummary{}, fmt.Errorf("scan scheduler run: %w", err)
	}
	item.StartedAt = parseStoredTime(startedAtRaw)
	item.CompletedAt = parseStoredTime(completedAtRaw)
	return item, nil
}

func ScanSchedulerEvent(scan func(dest ...any) error) (domain.SchedulerEvent, error) {
	var item domain.SchedulerEvent
	var createdAtRaw any
	if err := scan(&item.SchedulerID, &item.ID, &item.RunID, &item.TriggerID, &item.Type, &item.Level, &item.Message, &item.PayloadJSON, &item.LinkedSandboxID, &item.LinkedCellID, &item.LinkedAgentThreadID, &createdAtRaw); err != nil {
		return domain.SchedulerEvent{}, fmt.Errorf("scan scheduler event: %w", err)
	}
	item.CreatedAt = parseStoredTime(createdAtRaw)
	return item, nil
}

func ScanSchedulerBinding(scan func(dest ...any) error) (domain.SchedulerBinding, error) {
	var item domain.SchedulerBinding
	var createdAtRaw any
	var updatedAtRaw any
	if err := scan(&item.SchedulerID, &item.TriggerID, &item.SandboxID, &item.SandboxConfigHash, &createdAtRaw, &updatedAtRaw); err != nil {
		return domain.SchedulerBinding{}, fmt.Errorf("scan scheduler binding: %w", err)
	}
	item.CreatedAt = parseStoredTime(createdAtRaw)
	item.UpdatedAt = parseStoredTime(updatedAtRaw)
	return item, nil
}

func parseStoredSchedulerTriggerTime(value any) time.Time {
	switch typed := value.(type) {
	case nil:
		return time.Time{}
	case int64:
		return parseStoredUnixTimeAuto(typed)
	case int:
		return parseStoredUnixTimeAuto(int64(typed))
	case float64:
		return parseStoredUnixTimeAuto(int64(typed))
	case []byte:
		return parseStoredSchedulerTriggerTime(string(typed))
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return time.Time{}
		}
		if unixValue, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return parseStoredUnixTimeAuto(unixValue)
		}
		return parseStoredTime(trimmed)
	default:
		return parseStoredTime(value)
	}
}

func parseStoredTime(value any) time.Time {
	switch typed := value.(type) {
	case nil:
		return time.Time{}
	case int64:
		return parseStoredUnixTimeAuto(typed)
	case int:
		return parseStoredUnixTimeAuto(int64(typed))
	case float64:
		return parseStoredUnixTimeAuto(int64(typed))
	case []byte:
		return parseStoredTime(string(typed))
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return time.Time{}
		}
		if unixValue, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return parseStoredUnixTimeAuto(unixValue)
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
			if parsed, err := time.Parse(layout, trimmed); err == nil {
				return parsed.UTC()
			}
		}
	}
	return time.Time{}
}

func parseStoredUnixTimeAuto(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	if value >= 10_000_000_000 {
		return time.UnixMilli(value).UTC()
	}
	return time.Unix(value, 0).UTC()
}

func SelectSchedulerTriggerSQL() string {
	return `SELECT scheduler_id, trigger_id, kind, topic, interval_ms, enabled, auto_id, spec_json, next_fire_at, last_fired_at
        FROM scheduler_trigger`
}

func SelectSchedulerRunSQL() string {
	return `SELECT scheduler_id, run_id, trigger_id, trigger_kind, trigger_source, status, started_at, completed_at, duration_ms, error, result_json, payload_json, source_script_sha256, artifacts_dir
        FROM scheduler_run`
}

func SelectSchedulerEventSQL() string {
	return `SELECT scheduler_id, event_id, scheduler_run_id, trigger_id, type, level, message, payload_json, linked_sandbox_id, linked_cell_id, linked_agent_thread_id, created_at
        FROM scheduler_event`
}

func SelectSchedulerBindingSQL() string {
	return `SELECT scheduler_id, trigger_id, sandbox_id, sandbox_config_hash, created_at, updated_at FROM scheduler_sandbox_binding`
}

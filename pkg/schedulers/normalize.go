package schedulers

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"agent-compose/pkg/capabilities"
	driverpkg "agent-compose/pkg/driver"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/volumes"
)

func NormalizeScheduler(item domain.Scheduler, assignID bool) (domain.Scheduler, error) {
	now := time.Now().UTC()
	item.Summary.ID = strings.TrimSpace(item.Summary.ID)
	if assignID && item.Summary.ID == "" {
		item.Summary.ID = uuid.NewString()
	}
	if item.Summary.ID == "" {
		return domain.Scheduler{}, fmt.Errorf("scheduler id is required")
	}
	item.Summary.Name = strings.TrimSpace(item.Summary.Name)
	if item.Summary.Name == "" {
		item.Summary.Name = domain.DefaultSchedulerName(now)
	}
	item.Summary.Description = strings.TrimSpace(item.Summary.Description)
	runtime, err := domain.NormalizeSchedulerRuntime(item.Summary.Runtime)
	if err != nil {
		return domain.Scheduler{}, err
	}
	item.Summary.Runtime = runtime
	item.Script = strings.ReplaceAll(item.Script, "\r\n", "\n")
	if strings.TrimSpace(item.Script) == "" {
		return domain.Scheduler{}, fmt.Errorf("scheduler script is required")
	}
	item.Summary.WorkspaceID = strings.TrimSpace(item.Summary.WorkspaceID)
	item.Summary.AgentID = strings.TrimSpace(item.Summary.AgentID)
	item.Summary.Driver = strings.TrimSpace(item.Summary.Driver)
	if item.Summary.Driver != "" {
		driver, err := driverpkg.ResolveSandboxRuntimeDriver(item.Summary.Driver, item.Summary.Driver)
		if err != nil {
			return domain.Scheduler{}, err
		}
		item.Summary.Driver = driver
	}
	item.Summary.GuestImage = strings.TrimSpace(item.Summary.GuestImage)
	item.Summary.DefaultAgent = domain.NormalizeAgentKind(item.Summary.DefaultAgent)
	if item.Summary.DefaultAgent == "" {
		item.Summary.DefaultAgent = "codex"
	}
	item.Summary.SandboxPolicy = domain.NormalizeSchedulerSandboxPolicy(item.Summary.SandboxPolicy)
	item.Summary.ConcurrencyPolicy = domain.NormalizeSchedulerConcurrencyPolicy(item.Summary.ConcurrencyPolicy)
	item.Summary.CapsetIDs = capabilities.NormalizeCapsetIDs(item.Summary.CapsetIDs)
	item.Summary.ProjectID = strings.TrimSpace(item.Summary.ProjectID)
	item.Summary.AgentName = strings.TrimSpace(item.Summary.AgentName)
	item.Summary.ProjectSchedulerID = strings.TrimSpace(item.Summary.ProjectSchedulerID)
	if item.Summary.ProjectID == "" {
		item.Summary.ProjectRevision = 0
		item.Summary.AgentName = ""
		item.Summary.ProjectSchedulerID = ""
	} else {
		if item.Summary.AgentName == "" || item.Summary.ProjectSchedulerID == "" {
			return domain.Scheduler{}, fmt.Errorf("managed scheduler agent name and scheduler id are required")
		}
		if item.Summary.ProjectRevision < 0 {
			return domain.Scheduler{}, fmt.Errorf("managed scheduler project revision cannot be negative")
		}
	}
	item.EnvItems = domain.NormalizeEnvItems(item.EnvItems)
	volumes, err := volumes.NormalizeMountSpecs(item.Volumes)
	if err != nil {
		return domain.Scheduler{}, fmt.Errorf("scheduler volumes: %w", err)
	}
	item.Volumes = volumes
	item.Triggers = append([]domain.SchedulerTrigger(nil), item.Triggers...)
	return item, nil
}

func NormalizeSchedulerTrigger(schedulerID string, trigger domain.SchedulerTrigger) (domain.SchedulerTrigger, error) {
	trigger.SchedulerID = strings.TrimSpace(schedulerID)
	trigger.ID = strings.TrimSpace(trigger.ID)
	if trigger.SchedulerID == "" {
		return domain.SchedulerTrigger{}, fmt.Errorf("scheduler id is required")
	}
	if trigger.ID == "" {
		return domain.SchedulerTrigger{}, fmt.Errorf("scheduler trigger id is required")
	}
	kind, err := domain.NormalizeSchedulerTriggerKind(trigger.Kind)
	if err != nil {
		return domain.SchedulerTrigger{}, err
	}
	trigger.Kind = kind
	trigger.Topic = strings.TrimSpace(trigger.Topic)
	switch trigger.Kind {
	case domain.SchedulerTriggerKindInterval:
		if trigger.IntervalMs <= 0 {
			return domain.SchedulerTrigger{}, fmt.Errorf("scheduler interval trigger %s requires a positive interval", trigger.ID)
		}
		trigger.Topic = ""
	case domain.SchedulerTriggerKindEvent:
		if trigger.Topic == "" {
			return domain.SchedulerTrigger{}, fmt.Errorf("scheduler event trigger %s requires a topic", trigger.ID)
		}
		trigger.IntervalMs = 0
	case domain.SchedulerTriggerKindTimeout:
		if trigger.IntervalMs <= 0 {
			return domain.SchedulerTrigger{}, fmt.Errorf("scheduler timeout trigger %s requires a positive delay", trigger.ID)
		}
		trigger.Topic = ""
	case domain.SchedulerTriggerKindCron:
		trigger.Topic = ""
		trigger.IntervalMs = 0
		normalizedSpecJSON, err := NormalizeSchedulerCronSpecJSON(trigger.SpecJSON)
		if err != nil {
			return domain.SchedulerTrigger{}, fmt.Errorf("scheduler cron trigger %s: %w", trigger.ID, err)
		}
		trigger.SpecJSON = normalizedSpecJSON
	}
	trigger.SpecJSON = strings.TrimSpace(trigger.SpecJSON)
	if trigger.SpecJSON == "" {
		trigger.SpecJSON = "{}"
	}
	if !domain.TimeIsSet(trigger.NextFireAt) {
		trigger.NextFireAt = time.Time{}
	} else {
		trigger.NextFireAt = trigger.NextFireAt.UTC()
	}
	if !domain.TimeIsSet(trigger.LastFiredAt) {
		trigger.LastFiredAt = time.Time{}
	} else {
		trigger.LastFiredAt = trigger.LastFiredAt.UTC()
	}
	return trigger, nil
}

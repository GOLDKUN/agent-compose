package schedulers

import (
	domain "agent-compose/pkg/model"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cronlib "github.com/robfig/cron/v3"
)

const schedulerDefaultCronTimezone = "Local"

type schedulerCronSpec struct {
	Kind     string `json:"kind,omitempty"`
	Expr     string `json:"expr"`
	Timezone string `json:"timezone,omitempty"`
}

var schedulerCronParser = cronlib.NewParser(cronlib.SecondOptional | cronlib.Minute | cronlib.Hour | cronlib.Dom | cronlib.Month | cronlib.Dow | cronlib.Descriptor)

func schedulerCronSpecJSON(expr, timezone string) (string, error) {
	return SchedulerCronSpecJSON(expr, timezone)
}

func SchedulerCronSpecJSON(expr, timezone string) (string, error) {
	spec, err := normalizeSchedulerCronSpec(schedulerCronSpec{
		Kind:     domain.SchedulerTriggerKindCron,
		Expr:     expr,
		Timezone: timezone,
	})
	if err != nil {
		return "", err
	}
	return marshalJSONCompact(spec)
}

func NormalizeSchedulerCronSpecJSON(raw string) (string, error) {
	spec, err := parseSchedulerCronSpecJSON(raw)
	if err != nil {
		return "", err
	}
	return marshalJSONCompact(spec)
}

func SchedulerTriggerNextFireAt(now time.Time, trigger domain.SchedulerTrigger, fired bool) (time.Time, error) {
	now = now.UTC()
	switch strings.ToLower(strings.TrimSpace(trigger.Kind)) {
	case domain.SchedulerTriggerKindInterval:
		return domain.SchedulerTriggerScheduledAt(now, trigger.IntervalMs), nil
	case domain.SchedulerTriggerKindTimeout:
		if fired {
			return time.Time{}, nil
		}
		return domain.SchedulerTriggerScheduledAt(now, trigger.IntervalMs), nil
	case domain.SchedulerTriggerKindCron:
		spec, err := parseSchedulerCronSpecJSON(trigger.SpecJSON)
		if err != nil {
			return time.Time{}, err
		}
		location, err := time.LoadLocation(spec.Timezone)
		if err != nil {
			return time.Time{}, fmt.Errorf("load cron timezone %q: %w", spec.Timezone, err)
		}
		schedule, err := schedulerCronParser.Parse(spec.Expr)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse cron expression %q: %w", spec.Expr, err)
		}
		return schedule.Next(now.In(location)).UTC(), nil
	default:
		return time.Time{}, nil
	}
}

func SchedulerTriggerSource(trigger domain.SchedulerTrigger) string {
	switch strings.ToLower(strings.TrimSpace(trigger.Kind)) {
	case domain.SchedulerTriggerKindInterval:
		return fmt.Sprintf("interval:%d", trigger.IntervalMs)
	case domain.SchedulerTriggerKindTimeout:
		return fmt.Sprintf("timeout:%d", trigger.IntervalMs)
	case domain.SchedulerTriggerKindCron:
		spec, err := parseSchedulerCronSpecJSON(trigger.SpecJSON)
		if err != nil {
			return "cron"
		}
		return fmt.Sprintf("cron:%s@%s", spec.Expr, spec.Timezone)
	default:
		return ""
	}
}

func parseSchedulerCronSpecJSON(raw string) (schedulerCronSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return schedulerCronSpec{}, fmt.Errorf("cron spec is required")
	}
	var spec schedulerCronSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return schedulerCronSpec{}, fmt.Errorf("decode cron spec: %w", err)
	}
	return normalizeSchedulerCronSpec(spec)
}

func normalizeSchedulerCronSpec(spec schedulerCronSpec) (schedulerCronSpec, error) {
	spec.Kind = domain.SchedulerTriggerKindCron
	spec.Expr = strings.TrimSpace(spec.Expr)
	spec.Timezone = strings.TrimSpace(spec.Timezone)
	if spec.Expr == "" {
		return schedulerCronSpec{}, fmt.Errorf("cron expr is required")
	}
	if spec.Timezone == "" {
		spec.Timezone = schedulerDefaultCronTimezone
	}
	if _, err := time.LoadLocation(spec.Timezone); err != nil {
		return schedulerCronSpec{}, fmt.Errorf("load cron timezone %q: %w", spec.Timezone, err)
	}
	if _, err := schedulerCronParser.Parse(spec.Expr); err != nil {
		return schedulerCronSpec{}, fmt.Errorf("parse cron expression %q: %w", spec.Expr, err)
	}
	return spec, nil
}

func normalizeAgentKind(agent string) string {
	return domain.NormalizeAgentKind(agent)
}

func normalizeSchedulerSandboxPolicy(policy string) string {
	return domain.NormalizeSchedulerSandboxPolicy(policy)
}

func normalizeEnvItems(items []domain.SandboxEnvVar) []domain.SandboxEnvVar {
	return domain.NormalizeEnvItems(items)
}

func schedulerJSONResult(text, outputSchemaJSON, sourceName string) (any, error) {
	return JSONResult(text, outputSchemaJSON, sourceName)
}

func JSONResult(text, outputSchemaJSON, sourceName string) (any, error) {
	if strings.TrimSpace(outputSchemaJSON) == "" {
		return nil, nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON for outputSchema: %w", sourceName, err)
	}
	return parsed, nil
}

func marshalJSONCompact(value any) (string, error) {
	return domain.MarshalJSONCompact(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

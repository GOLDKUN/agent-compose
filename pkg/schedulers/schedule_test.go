package schedulers_test

import (
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/schedulers"
	"strings"
	"testing"
	"time"
)

func TestLoaderScheduleModelWorkflows(t *testing.T) {
	testLoaderScheduleModelWorkflows(t)
}

func testLoaderScheduleModelWorkflows(t *testing.T) {
	t.Helper()
	now := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)

	next, err := schedulers.SchedulerTriggerNextFireAt(now, domain.SchedulerTrigger{Kind: domain.SchedulerTriggerKindInterval, IntervalMs: 1500}, false)
	if err != nil {
		t.Fatalf("interval next fire returned error: %v", err)
	}
	if !next.Equal(now.Add(1500 * time.Millisecond)) {
		t.Fatalf("interval next fire = %s", next)
	}
	next, err = schedulers.SchedulerTriggerNextFireAt(now, domain.SchedulerTrigger{Kind: domain.SchedulerTriggerKindTimeout, IntervalMs: 2000}, true)
	if err != nil {
		t.Fatalf("fired timeout next fire returned error: %v", err)
	}
	if !next.IsZero() {
		t.Fatalf("fired timeout next fire = %s, want zero", next)
	}

	specJSON, err := schedulers.SchedulerCronSpecJSON("*/5 * * * *", "Asia/Shanghai")
	if err != nil {
		t.Fatalf("loaderCronSpecJSON returned error: %v", err)
	}
	next, err = schedulers.SchedulerTriggerNextFireAt(now, domain.SchedulerTrigger{Kind: domain.SchedulerTriggerKindCron, SpecJSON: specJSON}, false)
	if err != nil {
		t.Fatalf("cron next fire returned error: %v", err)
	}
	if next.IsZero() || !next.After(now) {
		t.Fatalf("cron next fire = %s, want after %s", next, now)
	}
	if source := schedulers.SchedulerTriggerSource(domain.SchedulerTrigger{Kind: domain.SchedulerTriggerKindCron, SpecJSON: specJSON}); source != "cron:*/5 * * * *@Asia/Shanghai" {
		t.Fatalf("cron source = %q", source)
	}
	if source := schedulers.SchedulerTriggerSource(domain.SchedulerTrigger{Kind: domain.SchedulerTriggerKindInterval, IntervalMs: 1000}); source != "interval:1000" {
		t.Fatalf("interval source = %q", source)
	}
	if source := schedulers.SchedulerTriggerSource(domain.SchedulerTrigger{Kind: domain.SchedulerTriggerKindTimeout, IntervalMs: 2000}); source != "timeout:2000" {
		t.Fatalf("timeout source = %q", source)
	}
	if source := schedulers.SchedulerTriggerSource(domain.SchedulerTrigger{Kind: domain.SchedulerTriggerKindCron, SpecJSON: `{bad json`}); source != "cron" {
		t.Fatalf("invalid cron source = %q", source)
	}

	normalized, err := schedulers.NormalizeLoaderCronSpecJSON(`{"expr":"@hourly"}`)
	if err != nil {
		t.Fatalf("normalizeLoaderCronSpecJSON returned error: %v", err)
	}
	if !strings.Contains(normalized, `"timezone":"UTC"`) {
		t.Fatalf("normalized cron spec = %q", normalized)
	}
	if _, err := schedulers.NormalizeLoaderCronSpecJSON(`{"expr":""}`); err == nil {
		t.Fatalf("empty cron expression returned nil error")
	}
	if _, err := schedulers.NormalizeLoaderCronSpecJSON(`{"expr":"* * * * *","timezone":"No/SuchZone"}`); err == nil {
		t.Fatalf("invalid cron timezone returned nil error")
	}
	if _, err := schedulers.SchedulerTriggerNextFireAt(now, domain.SchedulerTrigger{Kind: domain.SchedulerTriggerKindCron, SpecJSON: `{"expr":"bad cron"}`}, false); err == nil {
		t.Fatalf("invalid cron trigger returned nil error")
	}

	stableID := domain.SchedulerTriggerStableID(domain.SchedulerTriggerKindEvent, "runtime.*", 0, "function cb() {}", 1)
	if stableID != domain.SchedulerTriggerStableID(domain.SchedulerTriggerKindEvent, "runtime.*", 0, "function cb() {}", 1) {
		t.Fatalf("stable trigger id was not stable")
	}
	if domain.SchedulerSourceSHA("script") == domain.SchedulerSourceSHA("other") {
		t.Fatalf("loaderSourceSHA returned identical values for different scripts")
	}
	if !domain.SchedulerTriggerTopicMatches("runtime.*", "runtime.test") || !domain.SchedulerTriggerTopicMatches("runtime.test", "runtime.test") {
		t.Fatalf("expected topic patterns to match")
	}
	if domain.SchedulerTriggerTopicMatches("", "runtime.test") || domain.SchedulerTriggerTopicMatches("runtime.*", "") || domain.SchedulerTriggerTopicMatches("runtime.test", "runtime.other") {
		t.Fatalf("unexpected topic match")
	}

	if domain.NormalizeLoaderSandboxPolicy("new") != domain.SchedulerSandboxPolicyNew || domain.NormalizeLoaderSandboxPolicy("bad") != domain.SchedulerSandboxPolicySticky {
		t.Fatalf("session policy normalization failed")
	}
	if domain.NormalizeLoaderConcurrencyPolicy("allow") != domain.SchedulerConcurrencyPolicyParallel || domain.NormalizeLoaderConcurrencyPolicy("bad") != domain.SchedulerConcurrencyPolicySkip {
		t.Fatalf("concurrency policy normalization failed")
	}
	if domain.NormalizeLoaderRunStatus("failed") != domain.SchedulerRunStatusFailed || domain.NormalizeLoaderRunStatus("bad") != domain.SchedulerRunStatusRunning {
		t.Fatalf("run status normalization failed")
	}
	if !domain.TimeIsSet(now) || domain.NonZeroTimeUnixMilli(time.Time{}) != 0 || domain.NonZeroTimeUnixMilli(now) != now.UnixMilli() {
		t.Fatalf("time helpers returned unexpected values")
	}
	if !domain.SchedulerTriggerUsesSchedule(domain.SchedulerTriggerKindCron) || domain.SchedulerTriggerUsesSchedule(domain.SchedulerTriggerKindEvent) {
		t.Fatalf("schedule trigger helper returned unexpected values")
	}
	if !domain.SchedulerTriggerScheduledAt(now, 0).IsZero() || !domain.SchedulerTriggerScheduledAt(now, 1).Equal(now.Add(time.Millisecond)) {
		t.Fatalf("scheduled at helper returned unexpected values")
	}
	if domain.DefaultLoaderName(now) != "Scheduler 2026-06-02 09:00" {
		t.Fatalf("default loader name = %q", domain.DefaultLoaderName(now))
	}
	if script := domain.DefaultLoaderScript(); !strings.Contains(script, "function main") || !strings.Contains(script, "scheduler.interval") || !strings.Contains(script, "scheduler.on") {
		t.Fatalf("default loader script missing expected registrations: %s", script)
	}
}

package schedulers_test

import (
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/schedulers"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCronDefaultsToProcessLocalTimezone(t *testing.T) {
	if os.Getenv("AGENT_COMPOSE_CRON_LOCAL_TEST") == "1" {
		now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
		specJSON, err := schedulers.SchedulerCronSpecJSON("0 9 * * *", "")
		if err != nil {
			t.Fatal(err)
		}
		next, err := schedulers.SchedulerTriggerNextFireAt(now, domain.SchedulerTrigger{
			Kind:     domain.SchedulerTriggerKindCron,
			SpecJSON: specJSON,
		}, false)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 6, 2, 1, 0, 0, 0, time.UTC)
		if !next.Equal(want) {
			t.Fatalf("next local cron fire = %s, want %s", next, want)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestCronDefaultsToProcessLocalTimezone$")
	cmd.Env = append(os.Environ(), "AGENT_COMPOSE_CRON_LOCAL_TEST=1", "TZ=Asia/Shanghai")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("local timezone subprocess failed: %v\n%s", err, output)
	}
}

func TestSchedulerScheduleModelWorkflows(t *testing.T) {
	testSchedulerScheduleModelWorkflows(t)
}

func testSchedulerScheduleModelWorkflows(t *testing.T) {
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
		t.Fatalf("schedulerCronSpecJSON returned error: %v", err)
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

	normalized, err := schedulers.NormalizeSchedulerCronSpecJSON(`{"expr":"@hourly"}`)
	if err != nil {
		t.Fatalf("normalizeSchedulerCronSpecJSON returned error: %v", err)
	}
	if !strings.Contains(normalized, `"timezone":"Local"`) {
		t.Fatalf("normalized cron spec = %q", normalized)
	}
	explicitUTC, err := schedulers.SchedulerCronSpecJSON("0 9 * * *", "UTC")
	if err != nil || !strings.Contains(explicitUTC, `"timezone":"UTC"`) {
		t.Fatalf("explicit UTC cron spec = %q, err=%v", explicitUTC, err)
	}
	if _, err := schedulers.NormalizeSchedulerCronSpecJSON(`{"expr":""}`); err == nil {
		t.Fatalf("empty cron expression returned nil error")
	}
	if _, err := schedulers.NormalizeSchedulerCronSpecJSON(`{"expr":"* * * * *","timezone":"No/SuchZone"}`); err == nil {
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
		t.Fatalf("schedulerSourceSHA returned identical values for different scripts")
	}
	if !domain.SchedulerTriggerTopicMatches("runtime.*", "runtime.test") || !domain.SchedulerTriggerTopicMatches("runtime.test", "runtime.test") {
		t.Fatalf("expected topic patterns to match")
	}
	if domain.SchedulerTriggerTopicMatches("", "runtime.test") || domain.SchedulerTriggerTopicMatches("runtime.*", "") || domain.SchedulerTriggerTopicMatches("runtime.test", "runtime.other") {
		t.Fatalf("unexpected topic match")
	}

	if domain.NormalizeSchedulerSandboxPolicy("new") != domain.SchedulerSandboxPolicyNew || domain.NormalizeSchedulerSandboxPolicy("bad") != domain.SchedulerSandboxPolicySticky {
		t.Fatalf("session policy normalization failed")
	}
	if domain.NormalizeSchedulerConcurrencyPolicy("allow") != domain.SchedulerConcurrencyPolicyParallel || domain.NormalizeSchedulerConcurrencyPolicy("bad") != domain.SchedulerConcurrencyPolicySkip {
		t.Fatalf("concurrency policy normalization failed")
	}
	if domain.NormalizeSchedulerRunStatus("failed") != domain.SchedulerRunStatusFailed || domain.NormalizeSchedulerRunStatus("bad") != domain.SchedulerRunStatusRunning {
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
	if domain.DefaultSchedulerName(now) != "Scheduler 2026-06-02 09:00" {
		t.Fatalf("default scheduler name = %q", domain.DefaultSchedulerName(now))
	}
	if script := domain.DefaultSchedulerScript(); !strings.Contains(script, "function main") || !strings.Contains(script, "scheduler.interval") || !strings.Contains(script, "scheduler.on") {
		t.Fatalf("default scheduler script missing expected registrations: %s", script)
	}
}

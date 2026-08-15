package model_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/samber/do/v2"

	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/events"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sandboxes"
	"agent-compose/pkg/schedulers"
	"agent-compose/pkg/storage/configstore"
)

func TestModelBranchCoverageWorkflows(t *testing.T) {
	testModelBranchCoverageWorkflows(t)
}

func TestIntegrationModelBranchCoverageWorkflows(t *testing.T) {
	testModelBranchCoverageWorkflows(t)
}

func TestE2EModelBranchCoverageWorkflows(t *testing.T) {
	testModelBranchCoverageWorkflows(t)
}

func testModelBranchCoverageWorkflows(t *testing.T) {
	t.Helper()
	now := time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC)
	session := &domain.Sandbox{
		Summary: domain.SandboxSummary{
			ID:            "session-branch",
			Title:         "Branch Sandbox",
			TriggerSource: "script:scheduler-1",
			Driver:        driverpkg.RuntimeDriverDocker,
			VMStatus:      domain.VMStatusRunning,
			WorkspacePath: "/workspaces/branch",
			CreatedAt:     now,
			UpdatedAt:     now.Add(time.Minute),
		},
		WorkspaceID: "workspace-1",
		Workspace:   &domain.SandboxWorkspace{ID: "workspace-1", Name: "Workspace One", Type: "file"},
	}
	if !sandboxes.MatchesListOptions(session, domain.SandboxListOptions{
		SandboxType:        domain.SandboxTypeScript,
		TriggerSourceQuery: "scheduler",
		TitleQuery:         "branch",
		WorkspaceQuery:     "workspace one",
		Driver:             driverpkg.RuntimeDriverDocker,
		VMStatus:           domain.VMStatusRunning,
		CreatedFrom:        now.Add(-time.Second),
		CreatedTo:          now.Add(time.Second),
		UpdatedFrom:        now,
		UpdatedTo:          now.Add(2 * time.Minute),
	}) {
		t.Fatalf("session should match full list options")
	}
	for _, options := range []domain.SandboxListOptions{
		{SandboxType: domain.SandboxTypeManual},
		{TriggerSourceQuery: "missing"},
		{TitleQuery: "missing"},
		{WorkspaceQuery: "missing"},
		{Driver: driverpkg.RuntimeDriverBoxlite},
		{VMStatus: domain.VMStatusStopped},
		{CreatedFrom: now.Add(time.Second)},
		{CreatedTo: now.Add(-time.Second)},
		{UpdatedFrom: now.Add(2 * time.Minute)},
		{UpdatedTo: now.Add(-time.Second)},
	} {
		if sandboxes.MatchesListOptions(session, options) {
			t.Fatalf("session unexpectedly matched options %#v", options)
		}
	}
	if sandboxes.MatchesListOptions(nil, domain.SandboxListOptions{}) {
		t.Fatalf("nil session matched list options")
	}
	if got := sandboxes.NormalizeTriggerSource("", []domain.SandboxTag{{Name: "origin", Value: "loader"}, {Name: "loader_id", Value: "loader-9"}}); got != "script:loader-9" {
		t.Fatalf("NormalizeSandboxTriggerSource legacy tags = %q", got)
	}
	if got := sandboxes.NormalizeTriggerSource("", []domain.SandboxTag{{Name: "origin", Value: "scheduler"}, {Name: "scheduler_id", Value: "scheduler-9"}}); got != "script:scheduler-9" {
		t.Fatalf("NormalizeSandboxTriggerSource scheduler tags = %q", got)
	}
	if got := sandboxes.Paginate([]*domain.Sandbox{session}, 5, 10); got != nil {
		t.Fatalf("PaginateSandboxes beyond end = %#v", got)
	}
	offset, limit := sandboxes.NormalizeListBounds(-1, 0)
	if offset != 0 || limit != sandboxes.DefaultListLimit {
		t.Fatalf("NormalizeSandboxListBounds = %d/%d", offset, limit)
	}

	for _, runtime := range []string{"", domain.SchedulerRuntimeScheduler} {
		if got, err := domain.NormalizeSchedulerRuntime(runtime); err != nil || got != domain.SchedulerRuntimeScheduler {
			t.Fatalf("NormalizeSchedulerRuntime(%q) = %q/%v", runtime, got, err)
		}
	}
	for _, runtime := range []string{"qjs", "quickjs", "bad"} {
		if _, err := domain.NormalizeSchedulerRuntime(runtime); err == nil {
			t.Fatalf("NormalizeSchedulerRuntime(%q) returned nil error", runtime)
		}
	}
	for _, kind := range []string{domain.SchedulerTriggerKindInterval, domain.SchedulerTriggerKindEvent, domain.SchedulerTriggerKindTimeout, domain.SchedulerTriggerKindCron} {
		if got, err := domain.NormalizeSchedulerTriggerKind(kind); err != nil || got != kind {
			t.Fatalf("NormalizeSchedulerTriggerKind(%q) = %q/%v", kind, got, err)
		}
	}
	if _, err := domain.NormalizeSchedulerTriggerKind("bad"); err == nil {
		t.Fatalf("NormalizeSchedulerTriggerKind bad returned nil error")
	}
	if domain.NormalizeSchedulerSandboxPolicy("new") != domain.SchedulerSandboxPolicyNew || domain.NormalizeSchedulerSandboxPolicy("bad") != domain.SchedulerSandboxPolicySticky {
		t.Fatalf("NormalizeSchedulerSandboxPolicy returned unexpected values")
	}
	if domain.NormalizeSchedulerConcurrencyPolicy("allow") != domain.SchedulerConcurrencyPolicyParallel || domain.NormalizeSchedulerConcurrencyPolicy("bad") != domain.SchedulerConcurrencyPolicySkip {
		t.Fatalf("NormalizeSchedulerConcurrencyPolicy returned unexpected values")
	}
	for _, status := range []string{domain.SchedulerRunStatusRunning, domain.SchedulerRunStatusSucceeded, domain.SchedulerRunStatusFailed, domain.SchedulerRunStatusCanceled, domain.SchedulerRunStatusSkipped} {
		if domain.NormalizeSchedulerRunStatus(status) != status {
			t.Fatalf("NormalizeSchedulerRunStatus(%q) changed", status)
		}
	}
	if domain.NormalizeSchedulerRunStatus("bad") != domain.SchedulerRunStatusRunning {
		t.Fatalf("NormalizeSchedulerRunStatus bad did not default")
	}
	if !domain.SchedulerTriggerTopicMatches("agent-compose.session.*", "agent-compose.session.created") || domain.SchedulerTriggerTopicMatches("", "agent-compose.session.created") || domain.SchedulerTriggerTopicMatches("agent-compose.scheduler", "") {
		t.Fatalf("SchedulerTriggerTopicMatches returned unexpected values")
	}
	if domain.SchedulerTriggerTopicMatches("adp.session.*", "agent-compose.session.created") {
		t.Fatalf("legacy session wildcard matched agent-compose lifecycle topic")
	}
	if !domain.SchedulerTriggerUsesSchedule(domain.SchedulerTriggerKindCron) || domain.SchedulerTriggerUsesSchedule(domain.SchedulerTriggerKindEvent) {
		t.Fatalf("SchedulerTriggerUsesSchedule returned unexpected values")
	}
	if !domain.TimeIsSet(now) || domain.TimeIsSet(time.Time{}) || domain.NonZeroTimeUnixMilli(time.Time{}) != 0 || domain.NonZeroTimeUnixMilli(now) == 0 {
		t.Fatalf("time helper returned unexpected values")
	}
	if !domain.SchedulerTriggerScheduledAt(now, 10).After(now) || !domain.SchedulerTriggerScheduledAt(now, 0).IsZero() {
		t.Fatalf("SchedulerTriggerScheduledAt returned unexpected values")
	}
	if domain.DefaultSchedulerName(now) == "" || !strings.Contains(domain.DefaultSchedulerScript(), "scheduler.interval") || domain.SchedulerSourceSHA("script") == "" || domain.SchedulerTriggerStableID("kind", "topic", 1, "cb", 0) == "" {
		t.Fatalf("scheduler default/hash helpers returned empty values")
	}
	if err := events.ValidateTopicName("runtime.topic-1"); err != nil {
		t.Fatalf("ValidateTopicEventName returned error: %v", err)
	}
	for _, topic := range []string{" ", strings.Repeat("a", 129), "bad topic"} {
		if err := events.ValidateTopicName(topic); err == nil {
			t.Fatalf("ValidateTopicEventName(%q) returned nil error", topic)
		}
	}
	for _, source := range []string{domain.TopicEventSourceWebhook, " LOADER ", domain.TopicEventSourceSystem} {
		if events.NormalizeSource(source) == "" {
			t.Fatalf("NormalizeTopicEventSource(%q) returned empty", source)
		}
	}
	if events.NormalizeSource("bad") != "" {
		t.Fatalf("NormalizeTopicEventSource bad returned non-empty")
	}
	for _, status := range []string{"", domain.TopicEventDispatchPending, domain.TopicEventDispatchPublishing, domain.TopicEventDispatchPublishedToBus, domain.TopicEventDispatchNoSubscriber, domain.TopicEventDispatchRetrying, domain.TopicEventDispatchDeadLetter} {
		if events.NormalizeDispatchStatus(status) == "" {
			t.Fatalf("NormalizeTopicEventDispatchStatus(%q) returned empty", status)
		}
	}
	if events.NormalizeDispatchStatus("bad") != "" {
		t.Fatalf("NormalizeTopicEventDispatchStatus bad returned non-empty")
	}
	for _, status := range []string{domain.EventDeliveryStatusMatched, domain.EventDeliveryStatusRunStarted, domain.EventDeliveryStatusRunSucceeded, domain.EventDeliveryStatusRunFailed, domain.EventDeliveryStatusSkipped} {
		if events.NormalizeDeliveryStatus(status) != status {
			t.Fatalf("NormalizeEventDeliveryStatus(%q) changed", status)
		}
	}
	if events.NormalizeDeliveryStatus("bad") != "" || !strings.HasPrefix(events.PayloadSHA256(`{"ok":true}`), "sha256:") {
		t.Fatalf("topic event status/hash helpers returned unexpected values")
	}

	normalizedEnv := domain.NormalizeEnvItems([]domain.SandboxEnvVar{{Name: " B ", Value: "2"}, {Name: "A", Value: "1"}, {Name: "B", Value: "3"}, {Name: " ", Value: "skip"}})
	if len(normalizedEnv) != 2 || normalizedEnv[0].Name != "A" || normalizedEnv[1].Value != "3" {
		t.Fatalf("NormalizeEnvItems = %#v", normalizedEnv)
	}
	mergedEnv := domain.MergeEnvItems([]domain.SandboxEnvVar{{Name: "A", Value: "global"}}, []domain.SandboxEnvVar{{Name: "A", Value: "session"}, {Name: "B", Value: "session"}})
	if len(mergedEnv) != 2 || mergedEnv[0].Value != "session" || mergedEnv[1].Name != "B" {
		t.Fatalf("MergeEnvItems = %#v", mergedEnv)
	}
	if domain.MergeEnvItems(nil, nil) != nil {
		t.Fatalf("MergeEnvItems nil did not return nil")
	}
	envMap := domain.SandboxEnvMap([]domain.SandboxEnvVar{{Name: " A ", Value: "1"}, {Name: " ", Value: "skip"}}, []domain.SandboxEnvVar{{Name: "B", Value: "2"}})
	if envMap["A"] != "1" || envMap["B"] != "2" || domain.SandboxEnvMap([]domain.SandboxEnvVar{{Name: " "}}) != nil {
		t.Fatalf("SandboxEnvMap = %#v", envMap)
	}
	classified := domain.ResourceError(domain.ErrNotFound, "session", "session-1", "", errors.New("missing"))
	if !errors.Is(classified, domain.ErrNotFound) || !strings.Contains(classified.Error(), "missing") {
		t.Fatalf("classified error = %v", classified)
	}
	if err := domain.ClassifyError(domain.ErrInvalidArgument, "bad input", errors.New("cause")); !errors.Is(err, domain.ErrInvalidArgument) || !strings.Contains(err.Error(), "bad input: cause") {
		t.Fatalf("ClassifyError = %v", err)
	}
	if err := domain.ClassifyError(domain.ErrUnsupported, "feature unsupported", nil); !errors.Is(err, domain.ErrUnsupported) || !strings.Contains(err.Error(), "feature unsupported") {
		t.Fatalf("unsupported ClassifyError = %v", err)
	}
	workspace, err := configstore.NormalizeWorkspaceConfig(domain.WorkspaceConfig{Name: " Workspace ", Type: "FILE", ConfigJSON: "", Comment: " note "}, true)
	if err != nil || workspace.ID == "" || workspace.Type != "file" || workspace.ConfigJSON != "{}" || workspace.Comment != "note" {
		t.Fatalf("NormalizeWorkspaceConfig assign = %#v/%v", workspace, err)
	}
	for _, item := range []domain.WorkspaceConfig{
		{Name: "missing id", Type: "file"},
		{ID: "id", Type: "file"},
		{ID: "id", Name: "name"},
		{ID: "id", Name: "name", Type: "bad"},
	} {
		if _, err := configstore.NormalizeWorkspaceConfig(item, false); err == nil {
			t.Fatalf("NormalizeWorkspaceConfig(%#v) returned nil error", item)
		}
	}

	bus, err := schedulers.NewBus(do.New())
	if err != nil || bus.Events() == nil {
		t.Fatalf("NewBus = %#v/%v", bus, err)
	}
	if (&schedulers.Bus{}).Publish(domain.SchedulerTopicEvent{Topic: "runtime.test"}) {
		t.Fatalf("Publish on bus without channel succeeded")
	}
	if bus.Publish(domain.SchedulerTopicEvent{}) {
		t.Fatalf("Publish empty topic succeeded")
	}
	if !bus.Publish(domain.SchedulerTopicEvent{Topic: "runtime.test", Payload: map[string]any{"ok": true}}) {
		t.Fatalf("Publish valid event failed")
	}
	select {
	case event := <-bus.Events():
		if event.Topic != "runtime.test" {
			t.Fatalf("published event = %#v", event)
		}
	default:
		t.Fatalf("expected published event")
	}
	if (*schedulers.Bus)(nil).Events() != nil || (*schedulers.Bus)(nil).Publish(domain.SchedulerTopicEvent{Topic: "runtime.test"}) {
		t.Fatalf("nil scheduler bus helpers returned unexpected values")
	}

	ctx := context.Background()
	if err := ctx.Err(); err != nil {
		t.Fatalf("background context err = %v", err)
	}
}

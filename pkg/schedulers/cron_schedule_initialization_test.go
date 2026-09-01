package schedulers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestControllerRefreshInitializesCronWithoutNextFireTime(t *testing.T) {
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	cronSpec, err := SchedulerCronSpecJSON("0 9 * * *", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	store := newControllerTestStore()
	store.schedulers["scheduler-1"] = domain.Scheduler{
		Summary: domain.SchedulerSummary{ID: "scheduler-1", Enabled: true},
		Triggers: []domain.SchedulerTrigger{
			{ID: "cron", Kind: domain.SchedulerTriggerKindCron, Enabled: true, SpecJSON: cronSpec},
			{ID: "interval", Kind: domain.SchedulerTriggerKindInterval, Enabled: true, IntervalMs: 1000},
			{ID: "disabled", Kind: domain.SchedulerTriggerKindCron, Enabled: false, SpecJSON: cronSpec},
		},
	}
	controller := NewController(ControllerDependencies{Store: store, Now: func() time.Time { return now }})

	if err := controller.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	got := controller.CachedSchedulersMap()["scheduler-1"].Triggers
	want := time.Date(2026, 6, 2, 1, 0, 0, 0, time.UTC)
	if !got[0].NextFireAt.Equal(want) {
		t.Fatalf("initialized cron next fire = %s, want %s", got[0].NextFireAt, want)
	}
	if !got[1].NextFireAt.IsZero() || !got[2].NextFireAt.IsZero() {
		t.Fatalf("non-target next fire times = %s/%s", got[1].NextFireAt, got[2].NextFireAt)
	}
}

func TestControllerRefreshReportsCronInitializationFailures(t *testing.T) {
	tests := []struct {
		name      string
		specJSON  string
		storeErr  error
		wantError string
	}{
		{name: "invalid schedule", specJSON: `{"expr":"bad cron","timezone":"UTC"}`, wantError: "initialize scheduler cron trigger"},
		{name: "persist schedule", specJSON: `{"expr":"0 9 * * *","timezone":"UTC"}`, storeErr: errors.New("write failed"), wantError: "persist scheduler cron trigger schedule"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newControllerTestStore()
			store.nextFireErr = tt.storeErr
			store.schedulers["scheduler-1"] = domain.Scheduler{
				Summary:  domain.SchedulerSummary{ID: "scheduler-1", Enabled: true},
				Triggers: []domain.SchedulerTrigger{{ID: "cron", Kind: domain.SchedulerTriggerKindCron, Enabled: true, SpecJSON: tt.specJSON}},
			}
			controller := NewController(ControllerDependencies{Store: store})
			err := controller.Refresh(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Refresh error = %v, want %q", err, tt.wantError)
			}
		})
	}
}

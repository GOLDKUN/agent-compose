package schedulers

import (
	"testing"
	"time"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestEffectiveSchedulerRunTimeoutPrecedence(t *testing.T) {
	fallback := func(time.Duration) time.Duration { return 20 * time.Minute }
	tests := []struct {
		name      string
		scheduler domain.Scheduler
		override  time.Duration
		want      time.Duration
	}{
		{name: "explicit override", scheduler: domain.Scheduler{RunTimeout: time.Hour}, override: 5 * time.Minute, want: 5 * time.Minute},
		{name: "scheduler override", scheduler: domain.Scheduler{RunTimeout: time.Hour}, want: time.Hour},
		{name: "global fallback", want: 20 * time.Minute},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := effectiveSchedulerRunTimeout(test.scheduler, test.override, fallback); got != test.want {
				t.Fatalf("effective timeout = %s, want %s", got, test.want)
			}
		})
	}
	if got := effectiveSchedulerRunTimeout(domain.Scheduler{}, 0, nil); got != 20*time.Minute {
		t.Fatalf("hard-coded default timeout = %s, want 20m", got)
	}
}

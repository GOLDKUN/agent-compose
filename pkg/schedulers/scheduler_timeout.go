package schedulers

import (
	"time"

	domain "agent-compose/pkg/model"
)

func effectiveSchedulerRunTimeout(scheduler domain.Scheduler, override time.Duration, fallback func(time.Duration) time.Duration) time.Duration {
	if override > 0 {
		return override
	}
	if scheduler.RunTimeout > 0 {
		return scheduler.RunTimeout
	}
	if fallback != nil {
		return fallback(override)
	}
	return 20 * time.Minute
}

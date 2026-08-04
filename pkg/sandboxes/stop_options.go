package sandboxes

import "time"

type StopMode string

const (
	StopModeForce    StopMode = "force"
	StopModeGraceful StopMode = "graceful"
)

type StopOptions struct {
	Mode        StopMode
	GracePeriod time.Duration
}

type StopPreparationOutcome string

const (
	StopPreparationSkipped  StopPreparationOutcome = "skipped"
	StopPreparationGraceful StopPreparationOutcome = "graceful"
	StopPreparationTimeout  StopPreparationOutcome = "timeout"
	StopPreparationFailed   StopPreparationOutcome = "failed"
)

type StopPreparationResult struct {
	Outcome StopPreparationOutcome
	Error   error
}

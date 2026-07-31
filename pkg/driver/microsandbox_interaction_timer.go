//go:build linux && cgo && microsandboxcgo

package driver

import "time"

type microsandboxInteractionExitDrain struct {
	duration time.Duration
	timer    *time.Timer
	wait     <-chan time.Time
}

func newMicrosandboxInteractionExitDrain(duration time.Duration) *microsandboxInteractionExitDrain {
	if duration <= 0 {
		duration = microsandboxExecExitIdleGracePeriod
	}
	return &microsandboxInteractionExitDrain{duration: duration}
}

func (d *microsandboxInteractionExitDrain) reset() {
	d.stop()
	if d.timer == nil {
		d.timer = time.NewTimer(d.duration)
	} else {
		d.timer.Reset(d.duration)
	}
	d.wait = d.timer.C
}

func (d *microsandboxInteractionExitDrain) stop() {
	if d.timer == nil {
		return
	}
	if !d.timer.Stop() {
		select {
		case <-d.timer.C:
		default:
		}
	}
	d.wait = nil
}

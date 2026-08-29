package compose

import (
	"strings"
	"testing"
)

func TestNormalizeSchedulerRunTimeout(t *testing.T) {
	spec := mustParseCompose(t, `
name: timeout-test
agents:
  reviewer:
    provider: codex
    scheduler:
      run_timeout: 2h
`)
	normalized, err := Normalize(spec, NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := normalized.Agents[0].Scheduler.RunTimeout; got != "2h" {
		t.Fatalf("run_timeout = %q, want 2h", got)
	}
}

func TestNormalizeSchedulerRunTimeoutRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"not-a-duration", "0s", "-1m"} {
		t.Run(value, func(t *testing.T) {
			spec := mustParseCompose(t, "name: timeout-test\nagents:\n  reviewer:\n    scheduler:\n      run_timeout: "+value+"\n")
			_, err := Normalize(spec, NormalizeOptions{})
			if err == nil || !strings.Contains(err.Error(), "run_timeout") {
				t.Fatalf("Normalize error = %v, want run_timeout validation error", err)
			}
		})
	}
}

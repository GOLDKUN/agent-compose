package api

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	"agent-compose/pkg/sandboxes"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestSandboxStopOptions(t *testing.T) {
	tests := []struct {
		name     string
		request  *agentcomposev2.StopSandboxRequest
		want     sandboxes.StopOptions
		wantCode connect.Code
	}{
		{name: "unspecified remains force", request: &agentcomposev2.StopSandboxRequest{}, want: sandboxes.StopOptions{Mode: sandboxes.StopModeForce}},
		{name: "explicit force", request: &agentcomposev2.StopSandboxRequest{Mode: agentcomposev2.SandboxStopMode_SANDBOX_STOP_MODE_FORCE}, want: sandboxes.StopOptions{Mode: sandboxes.StopModeForce}},
		{name: "graceful default", request: &agentcomposev2.StopSandboxRequest{Mode: agentcomposev2.SandboxStopMode_SANDBOX_STOP_MODE_GRACEFUL}, want: sandboxes.StopOptions{Mode: sandboxes.StopModeGraceful}},
		{name: "graceful request period", request: &agentcomposev2.StopSandboxRequest{Mode: agentcomposev2.SandboxStopMode_SANDBOX_STOP_MODE_GRACEFUL, GracePeriod: durationpb.New(12 * time.Second)}, want: sandboxes.StopOptions{Mode: sandboxes.StopModeGraceful, GracePeriod: 12 * time.Second}},
		{name: "force rejects grace period", request: &agentcomposev2.StopSandboxRequest{Mode: agentcomposev2.SandboxStopMode_SANDBOX_STOP_MODE_FORCE, GracePeriod: durationpb.New(time.Second)}, wantCode: connect.CodeInvalidArgument},
		{name: "rejects zero period", request: &agentcomposev2.StopSandboxRequest{Mode: agentcomposev2.SandboxStopMode_SANDBOX_STOP_MODE_GRACEFUL, GracePeriod: durationpb.New(0)}, wantCode: connect.CodeInvalidArgument},
		{name: "rejects period above maximum", request: &agentcomposev2.StopSandboxRequest{Mode: agentcomposev2.SandboxStopMode_SANDBOX_STOP_MODE_GRACEFUL, GracePeriod: durationpb.New(maxSandboxGracePeriod + time.Second)}, wantCode: connect.CodeInvalidArgument},
		{name: "rejects unknown mode", request: &agentcomposev2.StopSandboxRequest{Mode: agentcomposev2.SandboxStopMode(99)}, wantCode: connect.CodeInvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := sandboxStopOptions(test.request)
			if test.wantCode != 0 {
				if connect.CodeOf(err) != test.wantCode {
					t.Fatalf("sandboxStopOptions() error = %v, code = %s, want %s", err, connect.CodeOf(err), test.wantCode)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("sandboxStopOptions() = %#v, %v; want %#v", got, err, test.want)
			}
		})
	}
}

func TestSandboxStopOutcomeToProto(t *testing.T) {
	tests := map[sandboxes.StopPreparationOutcome]agentcomposev2.SandboxStopOutcome{
		sandboxes.StopPreparationSkipped:  agentcomposev2.SandboxStopOutcome_SANDBOX_STOP_OUTCOME_FORCE,
		sandboxes.StopPreparationGraceful: agentcomposev2.SandboxStopOutcome_SANDBOX_STOP_OUTCOME_GRACEFUL,
		sandboxes.StopPreparationTimeout:  agentcomposev2.SandboxStopOutcome_SANDBOX_STOP_OUTCOME_FORCE_AFTER_GRACE_TIMEOUT,
		sandboxes.StopPreparationFailed:   agentcomposev2.SandboxStopOutcome_SANDBOX_STOP_OUTCOME_FORCE_AFTER_GRACE_ERROR,
	}
	for preparation, want := range tests {
		got := sandboxStopOutcomeToProto(sandboxes.StopOutcome{Preparation: sandboxes.StopPreparationResult{Outcome: preparation}})
		if got != want {
			t.Fatalf("sandboxStopOutcomeToProto(%q) = %s, want %s", preparation, got, want)
		}
	}
}

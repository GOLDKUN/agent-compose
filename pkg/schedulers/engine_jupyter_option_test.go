package schedulers

import (
	"context"
	"testing"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

// The scheduler sandbox-spawning APIs (scheduler.agent, scheduler.shell,
// scheduler.exec) accept a `jupyter: true` option that must reach the request
// struct so SchedulerSandboxRunner.Ensure can enable Jupyter on the sandbox.
func TestSchedulerJupyterOptionThreadsThroughRequests(t *testing.T) {
	run := func(t *testing.T, script string) *coverageEngineHost {
		t.Helper()
		host := &coverageEngineHost{state: map[string]string{}}
		if _, err := (&QJSSchedulerEngine{}).Execute(context.Background(), SchedulerExecutionRequest{
			Runtime: domain.SchedulerRuntimeScheduler,
			Script:  script,
		}, host); err != nil {
			t.Fatalf("Execute returned error: %v", err)
		}
		return host
	}

	t.Run("scheduler.agent jupyter true", func(t *testing.T) {
		host := run(t, `function main() { scheduler.agent("p", { jupyter: true }); }`)
		if len(host.agentCalls) != 1 || !host.agentCalls[0].JupyterEnabled {
			t.Fatalf("expected agent request with JupyterEnabled=true, got %#v", host.agentCalls)
		}
	})

	t.Run("scheduler.shell jupyter true", func(t *testing.T) {
		host := run(t, `function main() { scheduler.shell("echo hi", { jupyter: true }); }`)
		if len(host.commandCalls) != 1 || !host.commandCalls[0].JupyterEnabled {
			t.Fatalf("expected command request with JupyterEnabled=true, got %#v", host.commandCalls)
		}
	})

	t.Run("scheduler.exec jupyter true", func(t *testing.T) {
		host := run(t, `function main() { scheduler.exec({ command: "python3", jupyter: true }); }`)
		if len(host.commandCalls) != 1 || !host.commandCalls[0].JupyterEnabled {
			t.Fatalf("expected exec request with JupyterEnabled=true, got %#v", host.commandCalls)
		}
	})

	t.Run("omitted jupyter defaults false", func(t *testing.T) {
		host := run(t, `function main() { scheduler.agent("p", {}); scheduler.shell("echo hi", {}); }`)
		if len(host.agentCalls) != 1 || host.agentCalls[0].JupyterEnabled {
			t.Fatalf("expected agent request with JupyterEnabled=false, got %#v", host.agentCalls)
		}
		if len(host.commandCalls) != 1 || host.commandCalls[0].JupyterEnabled {
			t.Fatalf("expected command request with JupyterEnabled=false, got %#v", host.commandCalls)
		}
	})
}

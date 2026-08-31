package adapters

import (
	"context"
	"testing"

	driverpkg "github.com/chaitin/agent-compose/pkg/driver"
	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestSchedulerSandboxRunnerEnvironmentPrecedence(t *testing.T) {
	ctx := context.Background()
	bridge, driver := newTestSandboxRPCBridge(t)
	if _, err := bridge.configDB.ReplaceGlobalEnv(ctx, []domain.SandboxEnvVar{
		{Name: "GLOBAL_ONLY", Value: "global"},
		{Name: "SHARED", Value: "global"},
	}); err != nil {
		t.Fatalf("replace global env: %v", err)
	}
	agent := createNativeTestAgent(t, ctx, bridge.configDB, domain.AgentDefinition{
		ID:         "scheduler-env-agent",
		Name:       "scheduler-env-agent",
		Enabled:    true,
		Provider:   "codex",
		Driver:     driverpkg.RuntimeDriverDocker,
		GuestImage: "guest:latest",
		EnvItems: []domain.SandboxEnvVar{
			{Name: "AGENT_ONLY", Value: "agent"},
			{Name: "AGENT_VS_SCHEDULER", Value: "agent"},
			{Name: "SHARED", Value: "agent"},
		},
	})

	runner := NewSchedulerSandboxRunner(SchedulerSandboxRunnerDeps{
		Config:           bridge.config,
		Store:            bridge.store,
		ConfigDB:         bridge.configDB,
		WorkspaceEnsurer: bridge.workspaceEnsurer,
		Driver:           driver,
		Cap:              nil,
		VolumeResolver:   nil,
		Streams:          bridge.streams,
		Publisher:        nil,
		CapTokens:        nil,
		AgentExecutor:    bridge.agentExecutor,
	})
	scheduler := domain.Scheduler{
		Summary: domain.SchedulerSummary{
			ID:            "scheduler-env-precedence",
			Name:          "Scheduler env precedence",
			AgentID:       agent.ID,
			Driver:        driverpkg.RuntimeDriverDocker,
			GuestImage:    "guest:latest",
			SandboxPolicy: domain.SchedulerSandboxPolicyNew,
		},
		EnvItems: []domain.SandboxEnvVar{
			{Name: "AGENT_VS_SCHEDULER", Value: "scheduler", Secret: true},
			{Name: "SCHEDULER_ONLY", Value: "scheduler"},
			{Name: "SCHEDULER_VS_REQUEST", Value: "scheduler"},
			{Name: "SHARED", Value: "scheduler"},
		},
	}
	request := domain.SchedulerAgentRequest{SandboxEnv: []domain.SandboxEnvVar{
		{Name: "SCHEDULER_VS_REQUEST", Value: "request", Secret: true},
		{Name: "REQUEST_ONLY", Value: "request"},
		{Name: "SHARED", Value: "request", Secret: true},
	}}

	sandbox, _, err := runner.Ensure(ctx, scheduler, request, false)
	if err != nil {
		t.Fatalf("ensure scheduler sandbox: %v", err)
	}
	got := schedulerRunnerEnvItemsByName(domain.MergeEnvItems(sandbox.EnvItems, sandbox.ProviderEnvItems))
	for name, want := range map[string]domain.SandboxEnvVar{
		"GLOBAL_ONLY":          {Name: "GLOBAL_ONLY", Value: "global"},
		"AGENT_ONLY":           {Name: "AGENT_ONLY", Value: "agent"},
		"AGENT_VS_SCHEDULER":   {Name: "AGENT_VS_SCHEDULER", Value: "scheduler", Secret: true},
		"SCHEDULER_ONLY":       {Name: "SCHEDULER_ONLY", Value: "scheduler"},
		"SCHEDULER_VS_REQUEST": {Name: "SCHEDULER_VS_REQUEST", Value: "request", Secret: true},
		"REQUEST_ONLY":         {Name: "REQUEST_ONLY", Value: "request"},
		"SHARED":               {Name: "SHARED", Value: "request", Secret: true},
	} {
		if got[name] != want {
			t.Fatalf("effective env %s = %#v, want %#v", name, got[name], want)
		}
	}
	if _, ok := schedulerRunnerEnvItemsByName(sandbox.ProviderEnvItems)["GLOBAL_ONLY"]; ok {
		t.Fatal("global-only env was recorded as a sandbox provider override")
	}
}

func schedulerRunnerEnvItemsByName(items []domain.SandboxEnvVar) map[string]domain.SandboxEnvVar {
	byName := make(map[string]domain.SandboxEnvVar, len(items))
	for _, item := range domain.NormalizeEnvItems(items) {
		byName[item.Name] = item
	}
	return byName
}

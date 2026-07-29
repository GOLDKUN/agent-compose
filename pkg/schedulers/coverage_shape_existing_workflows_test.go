package schedulers_test

import "testing"

func TestIntegrationSchedulerRuntimeAndSchedulerWorkflows(t *testing.T) {
	t.Run("run executor lifecycle", TestRunExecutorLifecycleWorkflows)
	t.Run("event dispatcher", TestEventDispatcherWorkflows)
	t.Run("scheduler collect and dispatch", TestSchedulerCollectDueAndDispatch)
	t.Run("runtime host agent command llm and session rpc", TestRuntimeHostAgentCommandLLMAndSessionRPC)
	t.Run("runtime host project agent path", TestRuntimeHostProjectAgentPath)
	t.Run("schedule model", TestSchedulerScheduleModelWorkflows)
}

func TestE2ESchedulerRuntimeAndSchedulerWorkflows(t *testing.T) {
	TestIntegrationSchedulerRuntimeAndSchedulerWorkflows(t)
}

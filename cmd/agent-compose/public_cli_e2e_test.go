package main

import "testing"

func TestE2ECLIStreamingAndFailureWorkflows(t *testing.T) {
	t.Run("command branches", TestCLICommandBranchSweepWorkflows)
	t.Run("run output branches", TestCLIRunStreamAndDetailEdgeBranches)
	t.Run("run completion failures", TestCLIRunCompletionErrorBranches)
	t.Run("run command edges", TestCLIRunCommandAdditionalEdgeWorkflows)
	t.Run("exec interaction", TestCLIExecInteractiveUsesAttachExecClient)
	t.Run("exec prompt interaction", TestCLIExecPromptAttachUsesAttachExecClient)
	t.Run("daemon HTTP attach", TestDaemonHTTPClientAttachAgentRunBidiUsesH2C)
	t.Run("daemon TCP attach", TestDaemonTCPServerAttachAgentRunBidiUsesH2C)
}

package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	agentcomposev2 "github.com/chaitin/agent-compose/proto/agentcompose/v2"
)

func runComposeLogsForResourceID(cmd *cobra.Command, cli cliOptions, options composeLogsOptions) error {
	clients, err := newCLIServiceClients(cli)
	if err != nil {
		return err
	}
	target, err := resolveCLIResourceID(cmd, clients.resource, options.ResourceID, []agentcomposev2.ResourceKind{
		agentcomposev2.ResourceKind_RESOURCE_KIND_PROJECT,
		agentcomposev2.ResourceKind_RESOURCE_KIND_AGENT,
		agentcomposev2.ResourceKind_RESOURCE_KIND_RUN,
		agentcomposev2.ResourceKind_RESOURCE_KIND_SANDBOX,
	})
	if err != nil {
		return err
	}
	projectID := strings.TrimSpace(target.GetProjectId())
	projectName := strings.TrimSpace(target.GetProjectName())
	switch target.GetKind() {
	case agentcomposev2.ResourceKind_RESOURCE_KIND_PROJECT:
		projectID = target.GetId()
	case agentcomposev2.ResourceKind_RESOURCE_KIND_AGENT:
		options.AgentName = target.GetAgentName()
	case agentcomposev2.ResourceKind_RESOURCE_KIND_RUN:
		if options.RunID != "" || options.SandboxID != "" {
			return commandExitError{Code: exitCodeUsage, Err: fmt.Errorf("logs run id cannot be combined with --run or --sandbox")}
		}
		options.RunID = target.GetId()
	case agentcomposev2.ResourceKind_RESOURCE_KIND_SANDBOX:
		if options.RunID != "" || options.SandboxID != "" {
			return commandExitError{Code: exitCodeUsage, Err: fmt.Errorf("logs sandbox id cannot be combined with --run or --sandbox")}
		}
		options.SandboxID = target.GetId()
	default:
		return commandExitError{Code: exitCodeUsage, Err: fmt.Errorf("resource id cannot be used with logs")}
	}
	if options.RunID != "" {
		if !cli.JSON {
			return followRunLogStream(cmd.Context(), cmd.OutOrStdout(), clients.run, projectID, &agentcomposev2.RunSummary{RunId: options.RunID}, options)
		}
		run, err := getRunDetail(cmd.Context(), clients.run, projectID, options.RunID)
		if err != nil {
			return commandExitErrorForConnect(fmt.Errorf("get run %s: %w", options.RunID, err))
		}
		return writeLogsForRun(cmd.OutOrStdout(), run.Msg.GetRun(), cli.JSON, options)
	}
	return followOrPrintProjectLogs(cmd, cli, clients, projectID, projectName, options)
}

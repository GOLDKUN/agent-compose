package main

import (
	"time"

	"github.com/spf13/cobra"
)

type composeSandboxStopOptions struct {
	Graceful    bool
	Force       bool
	GracePeriod time.Duration
}

func newCLISandboxCommand(cli *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Manage project sandboxes",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	listOptions := composePSOptions{}
	listCmd := newCLISandboxListCommand(cli, "ls", "List project sandboxes", &listOptions)
	cmd.AddCommand(
		listCmd,
		newCLISandboxActionCommand(cli, "stop", "Stop one or more sandboxes", "stop", "stopped"),
		newCLISandboxActionCommand(cli, "resume", "Resume one or more sandboxes", "resume", "resumed"),
		newCLISandboxRemoveCommand(cli),
		newCLISandboxPruneCommand(cli),
	)
	return cmd
}

func newCLILegacySandboxCommands(cli *cliOptions) []*cobra.Command {
	return []*cobra.Command{
		newCLISandboxActionCommand(cli, "stop", "Stop one or more sandboxes", "stop", "stopped"),
		newCLISandboxActionCommand(cli, "resume", "Resume one or more sandboxes", "resume", "resumed"),
		newCLISandboxRemoveCommand(cli),
	}
}

func newCLISandboxActionCommand(cli *cliOptions, use, short, action, status string) *cobra.Command {
	options := composeSandboxStopOptions{}
	cmd := &cobra.Command{
		Use: use + " <sandbox> [<sandbox N>]", Short: short, Args: sandboxActionArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeSandboxActionCommand(cmd, *cli, action, status, options, args)
		},
	}
	if action == "stop" {
		cmd.Flags().BoolVar(&options.Graceful, "graceful", false, "Ask active guest executions to finish before stopping the sandbox")
		cmd.Flags().BoolVar(&options.Force, "force", false, "Stop immediately without waiting for guest execution cleanup")
		cmd.Flags().DurationVar(&options.GracePeriod, "grace-period", 0, "Maximum time to wait for graceful guest execution cleanup")
		cmd.MarkFlagsMutuallyExclusive("graceful", "force")
	}
	return cmd
}

func newCLISandboxRemoveCommand(cli *cliOptions) *cobra.Command {
	options := composeSandboxRemoveOptions{}
	cmd := &cobra.Command{
		Use: "rm <sandbox> [<sandbox N>]", Short: "Remove one or more sandboxes", Args: sandboxActionArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeSandboxRemoveCommand(cmd, *cli, options, args)
		},
	}
	cmd.Flags().BoolVar(&options.Force, "force", false, "Force remove running sandboxes")
	return cmd
}

func newCLISandboxPruneCommand(cli *cliOptions) *cobra.Command {
	options := composeSandboxPruneOptions{}
	cmd := &cobra.Command{
		Use: "prune", Short: "Prune stopped or failed sandboxes", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runComposeSandboxPruneCommand(cmd, *cli, options)
		},
	}
	addSandboxPruneFlags(cmd, &options)
	return cmd
}

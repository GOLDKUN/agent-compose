package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/chaitin/agent-compose/pkg/clientconfig"
)

type cliAuthLoginOptions struct {
	Token string
}

func newCLIAuthCommand(options *cliOptions) *cobra.Command {
	authCmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage daemon authentication",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	loginOptions := cliAuthLoginOptions{}
	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Verify and save a daemon token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCLIAuthLogin(cmd, options.Host, loginOptions)
		},
	}
	loginCmd.Flags().StringVar(&loginOptions.Token, "token", "", "Daemon bearer token")
	if err := loginCmd.MarkFlagRequired("token"); err != nil {
		panic(err)
	}
	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove a saved daemon token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCLIAuthLogout(cmd, options.Host)
		},
	}
	listCmd := &cobra.Command{
		Use:   "ls",
		Short: "List authenticated daemon sites",
		Args:  cobra.NoArgs,
		RunE:  runCLIAuthList,
	}
	authCmd.AddCommand(loginCmd, logoutCmd, listCmd)
	return authCmd
}

func runCLIAuthLogin(cmd *cobra.Command, hostFlag string, options cliAuthLoginOptions) error {
	token := strings.TrimSpace(options.Token)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return commandExitError{Code: exitCodeUsage, Err: fmt.Errorf("token must be a non-empty value without whitespace")}
	}
	clientConfig, err := resolveCLIClientEndpoint(hostFlag)
	if err != nil {
		return err
	}
	if clientConfig.UseUnixSocket {
		return commandExitError{Code: exitCodeUsage, Err: fmt.Errorf("auth login requires --host or AGENT_COMPOSE_HOST")}
	}
	clientConfig.AuthToken = token
	if _, err := fetchDaemonVersion(cmd.Context(), clientConfig); err != nil {
		var statusErr daemonHTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("authentication failed for %s: token was rejected (HTTP %d)", clientConfig.BaseURL, statusErr.StatusCode)
		}
		return fmt.Errorf("authenticate daemon %s: %w", clientConfig.BaseURL, err)
	}
	path, err := clientconfig.DefaultPath()
	if err != nil {
		return err
	}
	if err := clientconfig.SaveToken(path, clientConfig.BaseURL, token); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Authenticated %s\n", clientConfig.BaseURL)
	return err
}

func runCLIAuthLogout(cmd *cobra.Command, hostFlag string) error {
	clientConfig, err := resolveCLIClientEndpoint(hostFlag)
	if err != nil {
		return err
	}
	if clientConfig.UseUnixSocket {
		return commandExitError{Code: exitCodeUsage, Err: fmt.Errorf("auth logout requires --host or AGENT_COMPOSE_HOST")}
	}
	path, err := clientconfig.DefaultPath()
	if err != nil {
		return err
	}
	removed, err := clientconfig.RemoveToken(path, clientConfig.BaseURL)
	if err != nil {
		return err
	}
	if !removed {
		return commandExitError{Code: exitCodeUsage, Err: fmt.Errorf("no saved token for %s", clientConfig.BaseURL)}
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Logged out %s\n", clientConfig.BaseURL)
	return err
}

func runCLIAuthList(cmd *cobra.Command, _ []string) error {
	path, err := clientconfig.DefaultPath()
	if err != nil {
		return err
	}
	hosts, err := clientconfig.Hosts(path)
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No authenticated Agent-Compose sites.")
		return err
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Authenticated Agent-Compose sites:"); err != nil {
		return err
	}
	for _, host := range hosts {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "- %s\n", host); err != nil {
			return err
		}
	}
	return nil
}

func applyStoredCLIAuth(config *cliClientConfig) error {
	path, err := clientconfig.DefaultPath()
	if err != nil {
		return err
	}
	token, err := clientconfig.Token(path, config.BaseURL)
	if err != nil {
		return err
	}
	config.AuthToken = token
	return nil
}

type bearerAuthRoundTripper struct {
	token string
	next  http.RoundTripper
}

func (t bearerAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header.Set("Authorization", bearerScheme+t.token)
	return t.next.RoundTrip(cloned)
}

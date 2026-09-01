package main

import (
	"context"
	agentcomposev2 "github.com/chaitin/agent-compose/proto/agentcompose/v2"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

func TestCLIUpRejectsProjectNameBeforeReadingComposeOrApplyingProject(t *testing.T) {
	composePath := writeComposeFile(t, filepath.Join(t.TempDir(), "file-project"), `
name: file-project
agents: {}
`)
	withWorkingDir(t, t.TempDir())

	applyCalls := 0
	server := newComposeServiceStubServer(t, composeServiceStubs{
		project: projectServiceStub{
			applyProject: func(context.Context, *connect.Request[agentcomposev2.ApplyProjectRequest]) (*connect.Response[agentcomposev2.ApplyProjectResponse], error) {
				applyCalls++
				return connect.NewResponse(&agentcomposev2.ApplyProjectResponse{}), nil
			},
		},
	})
	t.Cleanup(server.Close)

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "top-level up with a different compose identity",
			args: []string{"--project-name", "selected-project", "--host", server.URL, "up", "--file", composePath},
		},
		{
			name: "grouped project up with a different compose identity",
			args: []string{"project", "up", "--project-name", "selected-project", "--host", server.URL, "--file", composePath},
		},
		{
			name: "matching compose identity is still unsupported",
			args: []string{"up", "--file", composePath, "--project-name", "file-project", "--host", server.URL},
		},
		{
			name: "project name shorthand is still unsupported",
			args: []string{"up", "--file", composePath, "-p", "file-project", "--host", server.URL},
		},
		{
			name: "implicit compose source is not read",
			args: []string{"up", "--project-name", "selected-project", "--host", server.URL},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, runCount, exitCode := executeCLICommand(test.args...)
			if exitCode != exitCodeUsage {
				t.Fatalf("up exit code = %d, want %d; stderr=%q", exitCode, exitCodeUsage, stderr)
			}
			if stdout != "" {
				t.Fatalf("up stdout = %q, want empty", stdout)
			}
			for _, want := range []string{"up does not support --project-name", "project identity comes from the compose file"} {
				if !strings.Contains(stderr, want) {
					t.Fatalf("up stderr %q does not contain %q", stderr, want)
				}
			}
			if runCount != 0 {
				t.Fatalf("daemon runner called %d times, want 0", runCount)
			}
		})
	}
	if applyCalls != 0 {
		t.Fatalf("ApplyProject called %d times, want 0", applyCalls)
	}
}

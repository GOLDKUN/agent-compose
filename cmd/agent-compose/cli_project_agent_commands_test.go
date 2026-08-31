package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	agentcomposev2 "github.com/chaitin/agent-compose/proto/agentcompose/v2"
)

func TestCLIProjectAndAgentCommandLayout(t *testing.T) {
	root := newRootCommand(nil, nil, func(context.Context) error { return nil })
	for _, args := range [][]string{
		{"project", "ls"},
		{"project", "up"},
		{"project", "down"},
		{"agent", "ls"},
		{"ls"},
		{"up"},
		{"down"},
	} {
		cmd, remaining, err := root.Find(args)
		if err != nil {
			t.Fatalf("find %q: %v", strings.Join(args, " "), err)
		}
		if len(remaining) != 0 {
			t.Fatalf("find %q remaining args = %q", strings.Join(args, " "), remaining)
		}
		if got := cmd.CommandPath(); got != "agent-compose "+strings.Join(args, " ") {
			t.Fatalf("command path = %q, want %q", got, "agent-compose "+strings.Join(args, " "))
		}
	}
}

func TestCLIAgentListAndTopLevelAlias(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent-list")
	composePath := writeComposeFile(t, dir, `
name: agent-list
agents:
  reviewer:
    provider: codex
`)
	project := testCLIProject("project-test", "agent-list", composePath)
	project.Agents[1].Model = ""
	project.Agents[1].ResolvedModel = "dev/gpt-5.5"
	project.Agents[1].ModelSource = agentcomposev2.AgentModelSource_AGENT_MODEL_SOURCE_DAEMON_DEFAULT
	server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
		getProject: func(_ context.Context, _ *connect.Request[agentcomposev2.GetProjectRequest]) (*connect.Response[agentcomposev2.GetProjectResponse], error) {
			return connect.NewResponse(&agentcomposev2.GetProjectResponse{Project: project}), nil
		},
	}})
	defer server.Close()

	for _, args := range [][]string{{"agent", "ls"}, {"ls"}} {
		stdout, stderr, _, code := executeCLICommand(append(args, "--file", composePath, "--host", server.URL)...)
		if code != 0 || stderr != "" {
			t.Fatalf("%s code/stderr = %d / %q", strings.Join(args, " "), code, stderr)
		}
		for _, want := range []string{"AGENT", "MODEL SOURCE", "reviewer", "worker", "gpt-test", "dev/gpt-5.5", "daemon_default", "boxlite"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("%s stdout = %q, want %q", strings.Join(args, " "), stdout, want)
			}
		}
	}

	stdout, stderr, _, code := executeCLICommand("agent", "ls", "--file", composePath, "--host", server.URL, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("agent ls --json code/stderr = %d / %q", code, stderr)
	}
	var output composeAgentListOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("decode agent list JSON: %v; stdout=%q", err, stdout)
	}
	if output.Project.Name != "agent-list" || len(output.Agents) != 2 {
		t.Fatalf("agent list JSON = %#v", output)
	}
	if worker := output.Agents[1]; worker.Model != "" || worker.ResolvedModel != "dev/gpt-5.5" || worker.ModelSource != "daemon_default" {
		t.Fatalf("worker model output = %#v", worker)
	}
}

func TestCLIProjectListUsesFormerTopLevelListBehavior(t *testing.T) {
	server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
		listProjects: func(_ context.Context, req *connect.Request[agentcomposev2.ListProjectsRequest]) (*connect.Response[agentcomposev2.ListProjectsResponse], error) {
			if req.Msg.GetLimit() != 1 || req.Msg.GetOffset() != 2 {
				t.Fatalf("project ls pagination = limit %d offset %d", req.Msg.GetLimit(), req.Msg.GetOffset())
			}
			return connect.NewResponse(&agentcomposev2.ListProjectsResponse{Projects: []*agentcomposev2.ProjectSummary{{Name: "listed-project"}}}), nil
		},
	}})
	defer server.Close()

	stdout, stderr, _, code := executeCLICommand("project", "ls", "--limit", "1", "--offset", "2", "--host", server.URL)
	if code != 0 || stderr != "" {
		t.Fatalf("project ls code/stderr = %d / %q", code, stderr)
	}
	if !strings.Contains(stdout, "listed-project") {
		t.Fatalf("project ls stdout = %q", stdout)
	}
}

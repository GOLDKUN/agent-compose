package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/chaitin/agent-compose/internal/projects"
	agentcomposev2 "github.com/chaitin/agent-compose/proto/agentcompose/v2"
	"github.com/chaitin/agent-compose/proto/agentcompose/v2/agentcomposev2connect"
)

func TestSchedulerProjectOutput(t *testing.T) {
	const (
		projectID   = "project-summary-output"
		projectName = "summary-output"
		sourcePath  = "/work/agent-compose.yml"
	)
	localProject := &agentcomposev2.Project{Summary: &agentcomposev2.ProjectSummary{
		ProjectId: projectID, Name: projectName, SourcePath: sourcePath,
	}}

	t.Run("权威摘要保留合法零值", func(t *testing.T) {
		project := testCLIProject(projectID, projectName, sourcePath)
		project.Summary.CurrentRevision = 0
		project.Summary.AgentCount = 0
		project.Summary.SchedulerCount = 0
		server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
			getProject: func(context.Context, *connect.Request[agentcomposev2.GetProjectRequest]) (*connect.Response[agentcomposev2.GetProjectResponse], error) {
				t.Fatal("权威摘要不应重复请求 GetProject")
				return nil, nil
			},
		}})
		t.Cleanup(server.Close)

		output := schedulerProjectOutput(t.Context(), agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL), composeRuntimeProject{
			composePath: sourcePath, project: project, summaryLoaded: true,
		})
		assertSchedulerProjectOutputMatchesSummary(t, output, project.Summary)
	})

	t.Run("仅身份模式按需补全摘要", func(t *testing.T) {
		project := testCLIProject(projectID, projectName, "/daemon/agent-compose.yml")
		var requests int
		server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
			getProject: func(_ context.Context, req *connect.Request[agentcomposev2.GetProjectRequest]) (*connect.Response[agentcomposev2.GetProjectResponse], error) {
				requests++
				if req.Msg.GetProject().GetProjectId() != projectID || req.Msg.GetIncludeSpec() {
					t.Fatalf("GetProject request = %#v", req.Msg)
				}
				return connect.NewResponse(&agentcomposev2.GetProjectResponse{Project: project}), nil
			},
		}})
		t.Cleanup(server.Close)

		output := schedulerProjectOutput(t.Context(), agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL), composeRuntimeProject{
			composePath: sourcePath, project: localProject,
		})
		if requests != 1 {
			t.Fatalf("GetProject calls = %d, want 1", requests)
		}
		assertSchedulerProjectOutputMatchesSummary(t, output, project.Summary)
	})

	t.Run("摘要不可用时省略未知字段", func(t *testing.T) {
		server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{}})
		t.Cleanup(server.Close)

		output := schedulerProjectOutput(t.Context(), agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL), composeRuntimeProject{
			composePath: sourcePath, project: localProject,
		})
		if output.ID != displayOpaqueID(projectID) || output.Name != projectName || output.ShortID != shortOpaqueID(projectID) || output.SourcePath != sourcePath {
			t.Fatalf("最小项目摘要 = %#v", output)
		}
		if output.CurrentRevision != nil || output.SpecHash != "" || output.AgentCount != nil || output.SchedulerCount != nil {
			t.Fatalf("最小项目摘要包含未知状态 = %#v", output)
		}
		assertSchedulerProjectFieldsOmitted(t, output)
	})
}

func assertSchedulerProjectJSONMatchesSummary(t *testing.T, data string, summary *agentcomposev2.ProjectSummary) {
	t.Helper()
	var envelope struct {
		Project composeSchedulerProjectOutput `json:"project"`
	}
	if err := json.Unmarshal([]byte(data), &envelope); err != nil {
		t.Fatalf("解析 scheduler JSON 输出: %v\n%s", err, data)
	}
	assertSchedulerProjectOutputMatchesSummary(t, envelope.Project, summary)
}

func assertSchedulerProjectJSONMinimal(t *testing.T, data string, summary *agentcomposev2.ProjectSummary) {
	t.Helper()
	var envelope struct {
		Project composeSchedulerProjectOutput `json:"project"`
	}
	if err := json.Unmarshal([]byte(data), &envelope); err != nil {
		t.Fatalf("解析 scheduler JSON 输出: %v\n%s", err, data)
	}
	project := envelope.Project
	if project.ID != displayOpaqueID(summary.GetProjectId()) || project.Name != summary.GetName() || project.ShortID != shortOpaqueID(summary.GetProjectId()) || project.SourcePath != summary.GetSourcePath() {
		t.Fatalf("最小项目摘要 = %#v, 本地摘要 = %#v", project, summary)
	}
	if project.CurrentRevision != nil || project.SpecHash != "" || project.AgentCount != nil || project.SchedulerCount != nil {
		t.Fatalf("最小项目摘要包含未知状态 = %#v", project)
	}
	assertSchedulerProjectFieldsOmitted(t, project)
}

func testSchedulerProjectSummary(t *testing.T, name, sourcePath string) *agentcomposev2.ProjectSummary {
	t.Helper()
	projectID, err := projects.StableProjectID(name, sourcePath)
	if err != nil {
		t.Fatalf("StableProjectID returned error: %v", err)
	}
	return testCLIProject(projectID, name, sourcePath).GetSummary()
}

func assertSchedulerProjectOutputMatchesSummary(t *testing.T, output composeSchedulerProjectOutput, summary *agentcomposev2.ProjectSummary) {
	t.Helper()
	if output.ID != displayOpaqueID(summary.GetProjectId()) || output.Name != summary.GetName() || output.ShortID != shortOpaqueID(summary.GetProjectId()) || output.SourcePath != summary.GetSourcePath() ||
		output.CurrentRevision == nil || *output.CurrentRevision != summary.GetCurrentRevision() || output.SpecHash != summary.GetSpecHash() ||
		output.AgentCount == nil || *output.AgentCount != summary.GetAgentCount() || output.SchedulerCount == nil || *output.SchedulerCount != summary.GetSchedulerCount() {
		t.Fatalf("scheduler 项目摘要 = %#v, daemon 摘要 = %#v", output, summary)
	}
}

func assertSchedulerProjectFieldsOmitted(t *testing.T, output composeSchedulerProjectOutput) {
	t.Helper()
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("编码 scheduler 项目摘要: %v", err)
	}
	for _, field := range []string{"current_revision", "spec_hash", "agent_count", "scheduler_count"} {
		if strings.Contains(string(data), `"`+field+`"`) {
			t.Fatalf("最小项目摘要不应包含 %q: %s", field, data)
		}
	}
}

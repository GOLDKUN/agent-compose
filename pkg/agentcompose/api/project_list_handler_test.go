package api

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	domain "github.com/chaitin/agent-compose/pkg/model"
	agentcomposev2 "github.com/chaitin/agent-compose/proto/agentcompose/v2"
)

func TestListProjectsUsesAggregatedResourceCounts(t *testing.T) {
	project := domain.ProjectRecord{ID: "project-1", Name: "Project", CurrentRevision: 2}
	store := &projectListStoreStub{
		result: domain.ProjectListResult{
			Projects: []domain.ProjectRecord{project},
			CountsByProjectID: map[string]domain.ProjectListCounts{
				project.ID: {AgentCount: 7, SchedulerCount: 3},
			},
			TotalCount: 1,
		},
	}
	handler := NewProjectHandler(nil, store, nil)

	response, err := handler.ListProjects(context.Background(), connect.NewRequest(&agentcomposev2.ListProjectsRequest{
		Query: "project", IncludeRemoved: true, Offset: 4, Limit: 25,
	}))
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(response.Msg.GetProjects()) != 1 || response.Msg.GetTotal() != 1 {
		t.Fatalf("ListProjects response = %#v", response.Msg)
	}
	summary := response.Msg.GetProjects()[0]
	if summary.GetAgentCount() != 7 || summary.GetSchedulerCount() != 3 {
		t.Fatalf("summary counts = agents %d schedulers %d", summary.GetAgentCount(), summary.GetSchedulerCount())
	}
	if store.listAgentsCalls != 0 || store.listSchedulersCalls != 0 {
		t.Fatalf("per-project queries = agents %d schedulers %d", store.listAgentsCalls, store.listSchedulersCalls)
	}
	if store.options.Query != "project" || !store.options.IncludeRemoved || store.options.Offset != 4 || store.options.Limit != 25 {
		t.Fatalf("ListProjects options = %#v", store.options)
	}
}

type projectListStoreStub struct {
	result              domain.ProjectListResult
	options             domain.ProjectListOptions
	listAgentsCalls     int
	listSchedulersCalls int
}

func (s *projectListStoreStub) GetProject(context.Context, string) (domain.ProjectRecord, error) {
	return domain.ProjectRecord{}, nil
}

func (s *projectListStoreStub) ListProjects(_ context.Context, options domain.ProjectListOptions) (domain.ProjectListResult, error) {
	s.options = options
	return s.result, nil
}

func (s *projectListStoreStub) ListProjectAgents(context.Context, string) ([]domain.ProjectAgentRecord, error) {
	s.listAgentsCalls++
	return nil, nil
}

func (s *projectListStoreStub) ListProjectSchedulers(context.Context, string) ([]domain.ProjectSchedulerRecord, error) {
	s.listSchedulersCalls++
	return nil, nil
}

func (s *projectListStoreStub) GetProjectRevision(context.Context, string, int64) (domain.ProjectRevisionRecord, error) {
	return domain.ProjectRevisionRecord{}, nil
}

package api

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	"agent-compose/pkg/projects"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

// projectDelegateRefSpy records the domain project reference PatchProject
// and RemoveProject were called with, so tests can assert the API boundary
// (ProjectHandler) is what validates and maps the transport ProjectRef,
// never the delegate.
type projectDelegateRefSpy struct {
	patchRef  projects.ProjectRef
	removeRef projects.ProjectRef
}

func (s *projectDelegateRefSpy) ValidateProject(context.Context, *connect.Request[agentcomposev2.ValidateProjectRequest]) (*connect.Response[agentcomposev2.ValidateProjectResponse], error) {
	return connect.NewResponse(&agentcomposev2.ValidateProjectResponse{}), nil
}

func (s *projectDelegateRefSpy) ApplyProject(context.Context, *connect.Request[agentcomposev2.ApplyProjectRequest]) (*connect.Response[agentcomposev2.ApplyProjectResponse], error) {
	return connect.NewResponse(&agentcomposev2.ApplyProjectResponse{}), nil
}

func (s *projectDelegateRefSpy) PatchProject(_ context.Context, _ *connect.Request[agentcomposev2.PatchProjectRequest], ref projects.ProjectRef) (*connect.Response[agentcomposev2.ApplyProjectResponse], error) {
	s.patchRef = ref
	return connect.NewResponse(&agentcomposev2.ApplyProjectResponse{}), nil
}

func (s *projectDelegateRefSpy) RemoveProject(_ context.Context, _ *connect.Request[agentcomposev2.RemoveProjectRequest], ref projects.ProjectRef) (*connect.Response[agentcomposev2.RemoveProjectResponse], error) {
	s.removeRef = ref
	return connect.NewResponse(&agentcomposev2.RemoveProjectResponse{}), nil
}

func (s *projectDelegateRefSpy) WatchProject(context.Context, *connect.Request[agentcomposev2.WatchProjectRequest], *connect.ServerStream[agentcomposev2.WatchProjectResponse]) error {
	return nil
}

func TestProjectHandlerMapsProjectRefBeforeDelegating(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  *agentcomposev2.ProjectRef
		want projects.ProjectRef
	}{
		{name: "project ID", ref: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: "project-1"}}, want: projects.ProjectRefByID("project-1")},
		{name: "name", ref: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_Name{Name: "Project"}}, want: projects.ProjectRefByName("Project")},
		{name: "source path", ref: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_SourcePath{SourcePath: "/repo/project.yml"}}, want: projects.ProjectRefBySourcePath("/repo/project.yml")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &projectDelegateRefSpy{}
			handler := NewProjectHandler(spy, nil, nil)
			ctx := context.Background()

			if _, err := handler.PatchProject(ctx, connect.NewRequest(&agentcomposev2.PatchProjectRequest{Project: tc.ref})); err != nil {
				t.Fatalf("PatchProject() error = %v", err)
			}
			if spy.patchRef != tc.want {
				t.Fatalf("delegate received PatchProject ref = %#v, want %#v", spy.patchRef, tc.want)
			}

			if _, err := handler.RemoveProject(ctx, connect.NewRequest(&agentcomposev2.RemoveProjectRequest{Project: tc.ref})); err != nil {
				t.Fatalf("RemoveProject() error = %v", err)
			}
			if spy.removeRef != tc.want {
				t.Fatalf("delegate received RemoveProject ref = %#v, want %#v", spy.removeRef, tc.want)
			}
		})
	}
}

// TestProjectHandlerRejectsMissingProjectSelectorBeforeDelegating asserts
// nil/unset selectors are rejected at the API boundary: the delegate is nil
// here, so if ProjectHandler forwarded the request instead of validating it
// first, these calls would panic rather than return InvalidArgument.
func TestProjectHandlerRejectsMissingProjectSelectorBeforeDelegating(t *testing.T) {
	handler := NewProjectHandler(nil, nil, nil)
	ctx := context.Background()

	if _, err := handler.PatchProject(ctx, connect.NewRequest(&agentcomposev2.PatchProjectRequest{})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("PatchProject() code = %v, want %v (error: %v)", connect.CodeOf(err), connect.CodeInvalidArgument, err)
	}
	if _, err := handler.RemoveProject(ctx, connect.NewRequest(&agentcomposev2.RemoveProjectRequest{Project: &agentcomposev2.ProjectRef{}})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("RemoveProject() code = %v, want %v (error: %v)", connect.CodeOf(err), connect.CodeInvalidArgument, err)
	}
}

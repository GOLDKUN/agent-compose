package api

import (
	"errors"
	"testing"

	"github.com/chaitin/agent-compose/internal/projects"
	domain "github.com/chaitin/agent-compose/pkg/model"
	agentcomposev2 "github.com/chaitin/agent-compose/proto/agentcompose/v2"
)

func TestProjectReferenceFromProto(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ref      *agentcomposev2.ProjectRef
		expected projects.ProjectRef
	}{
		{name: "project ID", ref: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: "project-1"}}, expected: projects.ProjectRefByID("project-1")},
		{name: "name", ref: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_Name{Name: "Project"}}, expected: projects.ProjectRefByName("Project")},
		{name: "source path", ref: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_SourcePath{SourcePath: "/repo/project.yml"}}, expected: projects.ProjectRefBySourcePath("/repo/project.yml")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := projectReferenceFromProto(tc.ref)
			if err != nil {
				t.Fatalf("projectReferenceFromProto() error = %v", err)
			}
			if got != tc.expected {
				t.Fatalf("projectReferenceFromProto() = %#v, want %#v", got, tc.expected)
			}
		})
	}
}

func TestProjectReferenceFromProtoRejectsMissingSelector(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  *agentcomposev2.ProjectRef
	}{
		{name: "nil reference"},
		{name: "unset selector", ref: &agentcomposev2.ProjectRef{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := projectReferenceFromProto(tc.ref)
			if !errors.Is(err, domain.ErrRequired) {
				t.Fatalf("projectReferenceFromProto() error = %v, want ErrRequired", err)
			}
		})
	}
}

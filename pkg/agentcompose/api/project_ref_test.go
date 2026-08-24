package api

import (
	"errors"
	"testing"

	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestProjectReferenceFromProto(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  *agentcomposev2.ProjectRef
	}{
		{name: "project ID", ref: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: "project-1"}}},
		{name: "name", ref: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_Name{Name: "Project"}}},
		{name: "source path", ref: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_SourcePath{SourcePath: "/repo/project.yml"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ProjectReferenceFromProto(tc.ref); err != nil {
				t.Fatalf("ProjectReferenceFromProto() error = %v", err)
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
			_, err := ProjectReferenceFromProto(tc.ref)
			if !errors.Is(err, domain.ErrRequired) {
				t.Fatalf("ProjectReferenceFromProto() error = %v, want ErrRequired", err)
			}
		})
	}
}

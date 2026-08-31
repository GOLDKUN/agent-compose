package api

import (
	"context"

	"agent-compose/internal/projects"
	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func resolveProjectReference(ctx context.Context, store projects.ProjectRefStore, ref *agentcomposev2.ProjectRef) (domain.ProjectRecord, error) {
	projectRef, err := projectReferenceFromProto(ref)
	if err != nil {
		return domain.ProjectRecord{}, err
	}
	return projects.ResolveProjectRef(ctx, store, projectRef)
}

// projectReferenceFromProto validates and maps a transport project reference
// to the domain representation used by project operations. It is the only
// place in the codebase that interprets the ProjectRef oneof; callers
// outside this package receive a validated projects.ProjectRef instead.
func projectReferenceFromProto(ref *agentcomposev2.ProjectRef) (projects.ProjectRef, error) {
	if ref == nil {
		return projects.ProjectRef{}, domain.ClassifyError(domain.ErrRequired, "project reference is required", nil)
	}
	switch selector := ref.GetSelector().(type) {
	case *agentcomposev2.ProjectRef_ProjectId:
		return projects.ProjectRefByID(selector.ProjectId), nil
	case *agentcomposev2.ProjectRef_Name:
		return projects.ProjectRefByName(selector.Name), nil
	case *agentcomposev2.ProjectRef_SourcePath:
		return projects.ProjectRefBySourcePath(selector.SourcePath), nil
	default:
		return projects.ProjectRef{}, domain.ClassifyError(domain.ErrRequired, "project selector is required", nil)
	}
}

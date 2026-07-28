package api

import (
	"context"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/projects"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func resolveProjectReference(ctx context.Context, store projects.ProjectRefStore, ref *agentcomposev2.ProjectRef) (domain.ProjectRecord, error) {
	return projects.ResolveProjectRef(ctx, store, projectReferenceFromProto(ref))
}

func projectReferenceFromProto(ref *agentcomposev2.ProjectRef) projects.ProjectRef {
	if ref == nil {
		return projects.ProjectRef{}
	}
	switch selector := ref.GetSelector().(type) {
	case *agentcomposev2.ProjectRef_ProjectId:
		return projects.ProjectRefByID(selector.ProjectId)
	case *agentcomposev2.ProjectRef_Name:
		return projects.ProjectRefByName(selector.Name)
	case *agentcomposev2.ProjectRef_SourcePath:
		return projects.ProjectRefBySourcePath(selector.SourcePath)
	default:
		return projects.ProjectRef{}
	}
}

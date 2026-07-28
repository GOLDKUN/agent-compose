package migrate

import (
	"context"
	"database/sql"
)

func prepareLegacyProjectConversion(
	ctx context.Context,
	db *sql.DB,
	targetRoot string,
	warnings []string,
	resumeSchedulerIDs map[string]string,
	agentIDs map[string]standaloneAgentIdentity,
	checkpoint databaseCheckpoint,
) ([]string, map[string]standaloneAgentIdentity, error) {
	identityWarnings, err := backfillLegacyProjectionIdentities(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	warnings, err = appendLegacyConversionWarnings(warnings, identityWarnings, resumeSchedulerIDs, agentIDs, checkpoint)
	if err != nil {
		return nil, nil, err
	}

	orphanWarnings, err := removeRetiredOrphanSchedulerProjections(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	warnings, err = appendLegacyConversionWarnings(warnings, orphanWarnings, resumeSchedulerIDs, agentIDs, checkpoint)
	if err != nil {
		return nil, nil, err
	}

	alignmentWarnings, err := alignManagedProjectionRevisions(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	warnings, err = appendLegacyConversionWarnings(warnings, alignmentWarnings, resumeSchedulerIDs, agentIDs, checkpoint)
	if err != nil {
		return nil, nil, err
	}

	standaloneWarnings, convertedAgentIDs, err := convertStandaloneV1(ctx, db, targetRoot, func(planned map[string]standaloneAgentIdentity) error {
		agentIDs = cloneStandaloneAgentIdentities(planned)
		if checkpoint == nil {
			return nil
		}
		return checkpoint(warnings, resumeSchedulerIDs, agentIDs)
	})
	if err != nil {
		return nil, nil, err
	}
	if convertedAgentIDs != nil {
		agentIDs = cloneStandaloneAgentIdentities(convertedAgentIDs)
	}
	warnings, err = appendLegacyConversionWarnings(warnings, standaloneWarnings, resumeSchedulerIDs, agentIDs, checkpoint)
	if err != nil {
		return nil, nil, err
	}
	return warnings, agentIDs, nil
}

func appendLegacyConversionWarnings(
	warnings, additions []string,
	schedulerIDs map[string]string,
	agentIDs map[string]standaloneAgentIdentity,
	checkpoint databaseCheckpoint,
) ([]string, error) {
	warnings = append(warnings, additions...)
	if checkpoint != nil && len(additions) > 0 {
		if err := checkpoint(warnings, schedulerIDs, agentIDs); err != nil {
			return nil, err
		}
	}
	return warnings, nil
}

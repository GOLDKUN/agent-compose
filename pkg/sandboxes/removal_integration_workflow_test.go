package sandboxes_test

import "testing"

func TestIntegrationRemovalCoordinatorRecoveryWorkflow(t *testing.T) {
	t.Run("resume staged removal", TestRemovalCoordinatorPersistsAndResumesStages)
	t.Run("recover deleting records", TestRemovalCoordinatorRecoveryOnlyProcessesDeletingRecords)
	t.Run("reject invalid ownership", TestRemovalCoordinatorRejectsInvalidOwnershipRecord)
	t.Run("prune records and residues", TestRemovalCoordinatorPruneSeparatesRecordsAndResidues)
	t.Run("serialize concurrent resume", TestRemovalCoordinatorSerializesConcurrentResume)
}

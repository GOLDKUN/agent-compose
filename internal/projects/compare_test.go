package projects

import (
	"testing"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestManagedAgentDefinitionChangeActionComparesEnabledState(t *testing.T) {
	tests := []struct {
		name     string
		existing bool
		current  bool
		want     string
	}{
		{name: "enabled remains enabled", existing: true, current: true, want: ChangeActionUnchanged},
		{name: "disabled remains disabled", existing: false, current: false, want: ChangeActionUnchanged},
		{name: "enabled becomes disabled", existing: true, current: false, want: ChangeActionUpdated},
		{name: "disabled becomes enabled", existing: false, current: true, want: ChangeActionUpdated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := domain.ProjectAgentRecord{ID: "agent-1", AgentName: "worker", SchedulerEnabled: tt.existing}
			current := existing
			current.SchedulerEnabled = tt.current
			if got := ProjectAgentChangeAction(existing, true, current); got != tt.want {
				t.Fatalf("ProjectAgentChangeAction() = %q, want %q", got, tt.want)
			}
		})
	}
}

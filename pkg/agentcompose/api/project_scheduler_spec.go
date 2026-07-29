package api

import (
	"encoding/json"

	"agent-compose/pkg/compose"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

// decodeProjectSchedulerSpec maps the normalized scheduler snapshot stored by
// the projects domain back to its transport representation.
func decodeProjectSchedulerSpec(raw string) (*agentcomposev2.SchedulerSpec, error) {
	var normalized compose.NormalizedSchedulerSpec
	if err := json.Unmarshal([]byte(raw), &normalized); err != nil {
		return nil, err
	}
	return SchedulerSpecToProto(&normalized), nil
}

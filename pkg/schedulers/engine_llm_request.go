package schedulers

import (
	"fmt"
	"strings"

	domain "agent-compose/pkg/model"

	"github.com/fastschema/qjs"
)

func parseSchedulerLLMRequest(args []*qjs.Value, state *schedulerExecutionState) (domain.SchedulerLLMRequest, error) {
	request := domain.SchedulerLLMRequest{}
	if len(args) < 2 || args[1] == nil || args[1].IsUndefined() || args[1].IsNull() {
		return request, nil
	}
	options, err := schedulerAgentOptionsWithoutSchema(state.jsonEncoder, args[1])
	if err != nil {
		return domain.SchedulerLLMRequest{}, fmt.Errorf("decode scheduler.llm options: %w", err)
	}
	if value, ok := options["model"].(string); ok {
		request.Model = strings.TrimSpace(value)
	}
	return request, nil
}

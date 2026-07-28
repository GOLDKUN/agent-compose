package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent-compose/pkg/execution"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/schedulers"
)

type SchedulerHostEvents struct {
	Controller *schedulers.Controller
}

// Adapter errors retain their established loader wording because they can
// surface through CLI and API error responses.
func (e SchedulerHostEvents) Add(ctx context.Context, schedulerID, runID, triggerID, eventType, level, message string, payload any, linkedSandboxID, linkedCellID, linkedAgentThreadID string) error {
	if e.Controller == nil {
		return fmt.Errorf("loader controller is unavailable")
	}
	return e.Controller.AddSchedulerEvent(ctx, schedulerID, runID, triggerID, eventType, level, message, payload, linkedSandboxID, linkedCellID, linkedAgentThreadID)
}

func (e SchedulerHostEvents) AddRecord(ctx context.Context, schedulerID, runID, triggerID, eventType, level, message string, payload any, linkedSandboxID, linkedCellID, linkedAgentThreadID string) (domain.SchedulerEvent, error) {
	if e.Controller == nil {
		return domain.SchedulerEvent{}, fmt.Errorf("loader controller is unavailable")
	}
	return e.Controller.AddSchedulerEventRecord(ctx, schedulerID, runID, triggerID, eventType, level, message, payload, linkedSandboxID, linkedCellID, linkedAgentThreadID)
}

type SchedulerHostAgentExecutor struct {
	Executor *AgentExecutor
}

func (e SchedulerHostAgentExecutor) ExecuteAgent(ctx context.Context, session *domain.Sandbox, request schedulers.HostAgentExecutionRequest) (domain.NotebookCell, error) {
	if e.Executor == nil {
		return domain.NotebookCell{}, fmt.Errorf("agent executor is unavailable")
	}
	cell, _, _, err := e.Executor.ExecuteAgentRequest(ctx, session, execution.ExecuteAgentRequest{
		Agent:             request.Provider,
		AgentDefinitionID: request.AgentDefinitionID,
		Model:             request.Model,
		RunID:             request.RunID,
		Message:           request.Prompt,
		Timeout:           request.Timeout,
		OutputSchemaJSON:  request.OutputSchemaJSON,
	})
	return cell, err
}

type SchedulerHostCommandExecutor struct {
	Executor *SchedulerCommandExecutor
}

func (e SchedulerHostCommandExecutor) ExecuteSchedulerCommand(ctx context.Context, session *domain.Sandbox, request domain.SchedulerCommandRequest) (domain.SchedulerCommandResult, error) {
	if e.Executor == nil {
		return domain.SchedulerCommandResult{}, fmt.Errorf("loader command executor is unavailable")
	}
	return e.Executor.ExecuteSchedulerCommand(ctx, session, request)
}

type SchedulerHostLLMRunner struct {
	Client *LLMClient
}

func (r SchedulerHostLLMRunner) Generate(ctx context.Context, prompt, model, outputSchema string) (domain.SchedulerLLMResult, error) {
	if r.Client == nil {
		return domain.SchedulerLLMResult{}, fmt.Errorf("llm client is unavailable")
	}
	result, err := r.Client.Generate(ctx, prompt, model, outputSchema)
	if err != nil {
		return domain.SchedulerLLMResult{}, err
	}
	return domain.SchedulerLLMResult{
		Text:         result.Text,
		Model:        result.Model,
		ResponseID:   result.ResponseID,
		FinishReason: result.FinishReason,
	}, nil
}

func SchedulerSandboxRPCLinkedSandboxID(method, requestJSON, responseJSON string) string {
	if value := schedulerSandboxIDFromJSON(responseJSON); value != "" {
		return value
	}
	if strings.TrimSpace(method) == "ListSandboxes" {
		return ""
	}
	return schedulerSandboxIDFromJSON(requestJSON)
}

func schedulerSandboxIDFromJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	if value, ok := payload["sandboxId"].(string); ok {
		return strings.TrimSpace(value)
	}
	sandboxValue, ok := payload["sandbox"].(map[string]any)
	if !ok {
		return ""
	}
	summaryValue, ok := sandboxValue["summary"].(map[string]any)
	if !ok {
		return ""
	}
	if value, ok := summaryValue["sandboxId"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

package runs

import (
	"strings"

	domain "agent-compose/pkg/model"
)

const (
	agentExecutionActivityName   = "agent_execution"
	commandExecutionActivityName = "command_execution"
)

type agentTurnProjection struct {
	Transcript      string
	FinalText       string
	FinalTextSource domain.AgentFinalTextSource
	Provider        string
	StopReason      string
}

func projectAgentTerminalEvents(run domain.ProjectRunRecord, cell domain.NotebookCell, assistant domain.SandboxEvent, execErr error) []domain.ProjectRunEventRecord {
	agent := firstNonEmpty(cell.Agent, run.AgentName)
	finalText := ""
	if execErr == nil && cell.Success {
		finalText = strings.TrimSpace(assistant.Message)
	}
	activityText := agentActivityText(cell.Output, finalText)
	events := make([]domain.ProjectRunEventRecord, 0, 2)
	if activityText != "" {
		events = append(events, domain.ProjectRunEventRecord{
			ID:         terminalActivityEventID(run.RunID),
			RunID:      run.RunID,
			Kind:       domain.ProjectRunEventKindAgentActivity,
			Text:       activityText,
			Agent:      agent,
			Name:       agentExecutionActivityName,
			Success:    cell.Success,
			ExitCode:   cell.ExitCode,
			StopReason: cell.StopReason,
		})
	}
	if finalText != "" {
		events = append(events, domain.ProjectRunEventRecord{
			ID:         terminalAgentEventID(run.RunID),
			RunID:      run.RunID,
			Kind:       domain.ProjectRunEventKindAgentMessage,
			Text:       finalText,
			Agent:      agent,
			Success:    true,
			ExitCode:   cell.ExitCode,
			StopReason: cell.StopReason,
		})
	}
	return events
}

func commandTerminalEvents(run domain.ProjectRunRecord, command string, result domain.ExecResult) []domain.ProjectRunEventRecord {
	text := commandActivityText(command, result.Output)
	if text == "" {
		return nil
	}
	return []domain.ProjectRunEventRecord{{
		ID:       terminalActivityEventID(run.RunID),
		RunID:    run.RunID,
		Kind:     domain.ProjectRunEventKindAgentActivity,
		Text:     text,
		Agent:    run.AgentName,
		Name:     commandExecutionActivityName,
		Success:  result.Success,
		ExitCode: result.ExitCode,
	}}
}

func attachedAgentTurnEvents(run domain.ProjectRunRecord, sequence uint64, frame []byte, turn agentTurnProjection) []domain.ProjectRunEventRecord {
	agent := firstNonEmpty(turn.Provider, run.AgentName)
	finalText := projectableFinalText(turn)
	activityText := agentActivityText(turn.Transcript, finalText)
	events := make([]domain.ProjectRunEventRecord, 0, 2)
	if activityText != "" {
		events = append(events, domain.ProjectRunEventRecord{
			ID:         attachedActivityEventID(run.RunID, sequence, frame),
			RunID:      run.RunID,
			Kind:       domain.ProjectRunEventKindAgentActivity,
			Text:       activityText,
			Agent:      agent,
			Name:       agentExecutionActivityName,
			Success:    true,
			StopReason: turn.StopReason,
		})
	}
	if finalText != "" {
		events = append(events, domain.ProjectRunEventRecord{
			ID:         attachedAgentEventID(run.RunID, sequence, frame),
			RunID:      run.RunID,
			Kind:       domain.ProjectRunEventKindAgentMessage,
			Text:       finalText,
			Agent:      agent,
			Success:    true,
			StopReason: turn.StopReason,
		})
	}
	return events
}

func terminalPromptTurnEvents(run domain.ProjectRunRecord, turn agentTurnProjection) []domain.ProjectRunEventRecord {
	agent := firstNonEmpty(turn.Provider, run.AgentName)
	finalText := projectableFinalText(turn)
	activityText := agentActivityText(turn.Transcript, finalText)
	events := make([]domain.ProjectRunEventRecord, 0, 2)
	if activityText != "" {
		events = append(events, domain.ProjectRunEventRecord{
			ID:         terminalActivityEventID(run.RunID),
			RunID:      run.RunID,
			Kind:       domain.ProjectRunEventKindAgentActivity,
			Text:       activityText,
			Agent:      agent,
			Name:       agentExecutionActivityName,
			Success:    true,
			StopReason: turn.StopReason,
		})
	}
	if finalText != "" {
		events = append(events, domain.ProjectRunEventRecord{
			ID:         terminalAgentEventID(run.RunID),
			RunID:      run.RunID,
			Kind:       domain.ProjectRunEventKindAgentMessage,
			Text:       finalText,
			Agent:      agent,
			Success:    true,
			StopReason: turn.StopReason,
		})
	}
	return events
}

func projectableFinalText(turn agentTurnProjection) string {
	finalText := strings.TrimSpace(turn.FinalText)
	if turn.FinalTextSource == domain.AgentFinalTextSourceProviderMessage {
		return finalText
	}
	if turn.FinalTextSource != "" || finalText == "" {
		return ""
	}
	if transcript := strings.TrimSpace(turn.Transcript); transcript != "" && transcript == finalText {
		return ""
	}
	return finalText
}

func agentActivityText(transcript, finalText string) string {
	transcript = strings.TrimSpace(transcript)
	finalText = strings.TrimSpace(finalText)
	if transcript == "" {
		return ""
	}
	if finalText == "" {
		return transcript
	}
	index := strings.LastIndex(transcript, finalText)
	if index < 0 {
		return transcript
	}
	before := strings.TrimSpace(transcript[:index])
	after := strings.TrimSpace(transcript[index+len(finalText):])
	return strings.TrimSpace(strings.Join(nonEmptyStrings(before, after), "\n"))
}

func commandActivityText(command, output string) string {
	command = strings.TrimSpace(command)
	output = strings.TrimSpace(output)
	parts := make([]string, 0, 2)
	if command != "" {
		parts = append(parts, "$ "+command)
	}
	if output != "" {
		parts = append(parts, output)
	}
	return strings.Join(parts, "\n")
}

func nonEmptyStrings(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

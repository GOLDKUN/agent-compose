package runs

import (
	"strings"
	"testing"

	domain "agent-compose/pkg/model"
)

func TestProjectAgentTerminalEventsSeparateTranscriptFromFinalMessage(t *testing.T) {
	run := domain.ProjectRunRecord{RunID: "run-agent-events", AgentName: "worker"}
	cell := domain.NotebookCell{
		Agent:      "codex",
		Output:     "checking weather\n$ curl https://weather.test\n{\"temperature\":26}\nBeijing is 26°C today.",
		Success:    true,
		StopReason: "completed",
	}
	events := projectAgentTerminalEvents(run, cell, domain.SandboxEvent{Message: "Beijing is 26°C today."}, nil)
	if len(events) != 2 {
		t.Fatalf("terminal events = %#v", events)
	}
	if events[0].Kind != domain.ProjectRunEventKindAgentActivity || !strings.Contains(events[0].Text, "curl https://weather.test") || strings.Contains(events[0].Text, "Beijing is 26°C today.") {
		t.Fatalf("activity event = %#v", events[0])
	}
	if events[1].Kind != domain.ProjectRunEventKindAgentMessage || events[1].Text != "Beijing is 26°C today." || strings.Contains(events[1].Text, "curl") {
		t.Fatalf("agent message event = %#v", events[1])
	}
}

func TestCommandTerminalEventsOnlyProjectActivity(t *testing.T) {
	events := commandTerminalEvents(
		domain.ProjectRunRecord{RunID: "run-command-events", AgentName: "worker"},
		"curl https://weather.test",
		domain.ExecResult{Output: "{\"temperature\":26}\n", Success: true},
	)
	if len(events) != 1 || events[0].Kind != domain.ProjectRunEventKindAgentActivity || events[0].Name != commandExecutionActivityName {
		t.Fatalf("command events = %#v", events)
	}
	if !strings.Contains(events[0].Text, "$ curl https://weather.test") || !strings.Contains(events[0].Text, `{"temperature":26}`) {
		t.Fatalf("command activity text = %q", events[0].Text)
	}
}

func TestAgentActivityTextKeepsTranscriptWhenFinalMessageIsUnavailable(t *testing.T) {
	transcript := "$ command\noutput"
	if got := agentActivityText(transcript, "different final"); got != transcript {
		t.Fatalf("activity text = %q, want %q", got, transcript)
	}
}

func TestFailedAgentTerminalEventsDoNotCreateAssistantMessage(t *testing.T) {
	run := domain.ProjectRunRecord{RunID: "run-agent-failure", AgentName: "worker"}
	cell := domain.NotebookCell{Agent: "codex", Output: "$ curl https://weather.test\nrequest failed", ExitCode: 1}
	events := projectAgentTerminalEvents(run, cell, domain.SandboxEvent{Message: "codex run failed"}, nil)
	if len(events) != 1 || events[0].Kind != domain.ProjectRunEventKindAgentActivity || events[0].Success {
		t.Fatalf("failed agent events = %#v", events)
	}
}

func TestTranscriptFallbackOnlyProjectsAgentActivity(t *testing.T) {
	transcript := "$ curl https://weather.test\n{\"temperature\":26}"
	events := attachedAgentTurnEvents(
		domain.ProjectRunRecord{RunID: "run-fallback", AgentName: "worker"},
		7,
		[]byte("fallback-frame"),
		agentTurnProjection{
			Transcript:      transcript,
			FinalText:       transcript,
			FinalTextSource: domain.AgentFinalTextSourceTranscriptFallback,
			Provider:        "codex",
		},
	)
	if len(events) != 1 || events[0].Kind != domain.ProjectRunEventKindAgentActivity {
		t.Fatalf("fallback events = %#v", events)
	}
	if events[0].Text != transcript {
		t.Fatalf("fallback activity text = %q, want %q", events[0].Text, transcript)
	}
}

func TestProviderFinalTextProjectsAgentMessage(t *testing.T) {
	events := terminalPromptTurnEvents(
		domain.ProjectRunRecord{RunID: "run-provider-message", AgentName: "worker"},
		agentTurnProjection{
			Transcript:      "$ command\noutput\nfinal answer",
			FinalText:       "final answer",
			FinalTextSource: domain.AgentFinalTextSourceProviderMessage,
			Provider:        "codex",
		},
	)
	if len(events) != 2 || events[1].Kind != domain.ProjectRunEventKindAgentMessage || events[1].Text != "final answer" {
		t.Fatalf("provider message events = %#v", events)
	}
}

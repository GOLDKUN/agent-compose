package execution

import (
	"strings"
	"testing"
	"unicode/utf8"

	domain "agent-compose/pkg/model"
)

func TestParseAgentAndCommandExecResultWorkflows(t *testing.T) {
	agentPayload := AgentResultPrefix + `{"provider":"codex","threadId":"agent-thread","stopReason":"done","finalText":"final","finalTextSource":"provider_message","transcript":"transcript"}`
	agent, err := ParseAgentExecResult("codex", domain.ExecResult{Stdout: "logs\n" + agentPayload, ExitCode: 0, Success: true})
	if err != nil {
		t.Fatalf("ParseAgentExecResult returned error: %v", err)
	}
	if agent.Agent != "codex" || agent.ThreadID != "agent-thread" || agent.DisplayOutput != "transcript" || agent.FinalTextSource != domain.AgentFinalTextSourceProviderMessage {
		t.Fatalf("agent result = %#v", agent)
	}
	if _, err := ParseAgentExecResult("codex", domain.ExecResult{Stderr: strings.Repeat("x", 300)}); err == nil || !strings.Contains(err.Error(), "...") {
		t.Fatalf("expected summarized failure, got %v", err)
	}
	if stripped := StripAgentResultPayload("hello\n" + agentPayload); stripped != "hello\n" {
		t.Fatalf("stripped = %q", stripped)
	}
	sanitized := SanitizeAgentExecResult(domain.ExecResult{Stdout: "stdout\n" + agentPayload, Output: "output\n" + agentPayload})
	if strings.Contains(sanitized.Stdout, AgentResultPrefix) || strings.Contains(sanitized.Output, AgentResultPrefix) {
		t.Fatalf("sanitized = %#v", sanitized)
	}

	commandPayload := CommandResultPrefix + `{"stdout":"out","stderr":"err","output":"out","exitCode":7,"success":false}`
	if stripped := StripCommandResultPayload("out\n" + commandPayload); stripped != "out\n" {
		t.Fatalf("command stripped = %q", stripped)
	}
	if stripped := StripCommandResultPayload(commandPayload); stripped != "" {
		t.Fatalf("command payload stripped = %q", stripped)
	}
	command, err := ParseCommandExecResult(domain.ExecResult{Stdout: "noise\n" + commandPayload})
	if err != nil {
		t.Fatalf("ParseCommandExecResult returned error: %v", err)
	}
	if command.ExitCode != 7 || command.Stdout != "out" || command.Success {
		t.Fatalf("command result = %#v", command)
	}
	joinedCommand, err := ParseCommandExecResult(domain.ExecResult{Stdout: "no-newline" + commandPayload})
	if err != nil {
		t.Fatalf("ParseCommandExecResult joined payload returned error: %v", err)
	}
	if joinedCommand.ExitCode != 7 || joinedCommand.Stdout != "out" || joinedCommand.Success {
		t.Fatalf("joined command result = %#v", joinedCommand)
	}
	if _, err := ParseCommandExecResult(domain.ExecResult{Stdout: "noise"}); err == nil {
		t.Fatalf("expected missing command payload error")
	}
}

// TestParseCommandExecResultIgnoresPrefixQuotedInsidePayload guards against a
// regression where the command's own captured output legitimately quotes
// CommandResultPrefix (e.g. a GitHub issue body describing this protocol).
// The genuine marker is always the leftmost occurrence on the line, because
// the wrapper prints its own prefix before embedding the captured output;
// picking the rightmost occurrence (as a naive strings.LastIndex scan would)
// lands inside the quoted text instead of the real JSON payload.
func TestParseCommandExecResultIgnoresPrefixQuotedInsidePayload(t *testing.T) {
	quotedOutput := "docs mention " + CommandResultPrefix + " twice, " + CommandResultPrefix + " here too"
	payload := CommandResultPrefix + `{"stdout":"` + quotedOutput + `","stderr":"","output":"","exitCode":0,"success":true}`
	command, err := ParseCommandExecResult(domain.ExecResult{Stdout: "prep\n" + payload})
	if err != nil {
		t.Fatalf("ParseCommandExecResult returned error: %v", err)
	}
	if !command.Success || command.ExitCode != 0 || command.Stdout != quotedOutput {
		t.Fatalf("command result = %#v", command)
	}
}

func TestParseAgentExecResultClassifiesFinalTextFallback(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		wantSource domain.AgentFinalTextSource
	}{
		{name: "explicit provider message", payload: `{"finalText":"answer","finalTextSource":"provider_message","transcript":"answer"}`, wantSource: domain.AgentFinalTextSourceProviderMessage},
		{name: "explicit transcript fallback", payload: `{"finalText":"$ command\\noutput","finalTextSource":"transcript_fallback","transcript":"$ command\\noutput"}`, wantSource: domain.AgentFinalTextSourceTranscriptFallback},
		{name: "legacy transcript fallback", payload: `{"finalText":"$ command\\noutput","transcript":"$ command\\noutput"}`, wantSource: domain.AgentFinalTextSourceTranscriptFallback},
		{name: "legacy distinct final", payload: `{"finalText":"answer","transcript":"$ command\\noutput\\nanswer"}`, wantSource: domain.AgentFinalTextSourceProviderMessage},
		{name: "unknown source", payload: `{"finalText":"answer","finalTextSource":"future_source","transcript":"activity\\nanswer"}`, wantSource: domain.AgentFinalTextSourceTranscriptFallback},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseAgentExecResult("codex", domain.ExecResult{Stdout: AgentResultPrefix + tt.payload, Success: true})
			if err != nil {
				t.Fatalf("ParseAgentExecResult returned error: %v", err)
			}
			if result.FinalTextSource != tt.wantSource {
				t.Fatalf("final text source = %q, want %q", result.FinalTextSource, tt.wantSource)
			}
		})
	}
}

func TestParseAgentExecResultClassifiesCancelledResultAsUnsuccessful(t *testing.T) {
	result, err := ParseAgentExecResult("codex", domain.ExecResult{
		Stdout:   AgentResultPrefix + `{"provider":"codex","threadId":"thread-1","stopReason":"cancelled","finalText":"partial","transcript":"partial"}`,
		ExitCode: 0,
		Success:  true,
	})
	if err != nil {
		t.Fatalf("ParseAgentExecResult() error = %v", err)
	}
	if result.Success || result.ExitCode == 0 || result.StopReason != "cancelled" || result.FinalText != "partial" || result.ThreadID != "thread-1" {
		t.Fatalf("ParseAgentExecResult() = %#v", result)
	}
}

func TestSummarizeAgentExecFailurePreservesUTF8(t *testing.T) {
	detail := SummarizeAgentExecFailure(domain.ExecResult{Stderr: strings.Repeat("界", 241)})
	if !strings.HasSuffix(detail, "...") {
		t.Fatalf("summary should be truncated: %q", detail)
	}
	if !utf8.ValidString(detail) {
		t.Fatalf("summary is not valid UTF-8: %q", detail)
	}
	if got := len([]rune(strings.TrimSuffix(detail, "..."))); got != 240 {
		t.Fatalf("summary has %d content runes, want 240", got)
	}
}

func TestFilterCommandStreamChunk(t *testing.T) {
	commandPayload := CommandResultPrefix + `{"stdout":"out","stderr":"","output":"out","exitCode":0,"success":true}`
	filtered, visible := FilterCommandStreamChunk(domain.ExecChunk{
		Text:   "visible\n" + commandPayload,
		Stream: domain.StdioStdout,
	})
	if !visible {
		t.Fatalf("expected command chunk to remain visible")
	}
	if filtered.Text != "visible\n" || filtered.Stream != domain.StdioStdout {
		t.Fatalf("filtered command chunk = %#v", filtered)
	}

	filtered, visible = FilterCommandStreamChunk(domain.ExecChunk{
		Text:   commandPayload,
		Stream: domain.StdioStderr,
	})
	if visible {
		t.Fatalf("payload-only command chunk should be hidden: %#v", filtered)
	}
	if filtered.Text != "" || filtered.Stream != domain.StdioStderr {
		t.Fatalf("payload-only command chunk = %#v", filtered)
	}

	filtered, visible = FilterCommandStreamChunk(domain.ExecChunk{
		Text:   "stderr transcript",
		Stream: domain.StdioStderr,
	})
	if !visible || filtered.Text != "stderr transcript" || filtered.Stream != domain.StdioStderr {
		t.Fatalf("stderr transcript command chunk = %#v visible=%v", filtered, visible)
	}

	unknownStream := domain.StdioStream("future")
	filtered, visible = FilterCommandStreamChunk(domain.ExecChunk{
		Text:   "unknown stream transcript",
		Stream: unknownStream,
	})
	if !visible || filtered.Text != "unknown stream transcript" || filtered.Stream != unknownStream {
		t.Fatalf("unknown stream command chunk = %#v visible=%v", filtered, visible)
	}
}

func TestFilterAgentStreamChunk(t *testing.T) {
	agentPayload := AgentResultPrefix + `{"provider":"codex","finalText":"done"}`
	filtered, visible := FilterAgentStreamChunk(domain.ExecChunk{
		Text:   "stdout transcript\n" + agentPayload,
		Stream: domain.StdioStdout,
	})
	if !visible {
		t.Fatalf("expected agent stdout prefix to remain visible")
	}
	if filtered.Text != "stdout transcript\n" || filtered.Stream != domain.StdioStdout {
		t.Fatalf("filtered agent stdout chunk = %#v", filtered)
	}

	filtered, visible = FilterAgentStreamChunk(domain.ExecChunk{
		Text:   agentPayload,
		Stream: domain.StdioStdout,
	})
	if visible {
		t.Fatalf("payload-only agent chunk should be hidden: %#v", filtered)
	}
	if filtered.Text != "" || filtered.Stream != domain.StdioStdout {
		t.Fatalf("payload-only agent chunk = %#v", filtered)
	}

	filtered, visible = FilterAgentStreamChunk(domain.ExecChunk{
		Text:   "stderr transcript",
		Stream: domain.StdioStderr,
	})
	if !visible || filtered.Text != "stderr transcript" || filtered.Stream != domain.StdioStderr {
		t.Fatalf("stderr transcript agent chunk = %#v visible=%v", filtered, visible)
	}

	unknownStream := domain.StdioStream("future")
	filtered, visible = FilterAgentStreamChunk(domain.ExecChunk{
		Text:   "unknown stream transcript",
		Stream: unknownStream,
	})
	if !visible || filtered.Text != "unknown stream transcript" || filtered.Stream != unknownStream {
		t.Fatalf("unknown stream agent chunk = %#v visible=%v", filtered, visible)
	}
}

func TestIntegrationParseAgentAndCommandExecResultWorkflows(t *testing.T) {
	TestParseAgentAndCommandExecResultWorkflows(t)
}

func TestE2EParseAgentAndCommandExecResultWorkflows(t *testing.T) {
	TestParseAgentAndCommandExecResultWorkflows(t)
}

func TestIntegrationParseCommandExecResultIgnoresPrefixQuotedInsidePayload(t *testing.T) {
	TestParseCommandExecResultIgnoresPrefixQuotedInsidePayload(t)
}

func TestE2EParseCommandExecResultIgnoresPrefixQuotedInsidePayload(t *testing.T) {
	TestParseCommandExecResultIgnoresPrefixQuotedInsidePayload(t)
}

package execution

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-compose/pkg/compose"
	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
)

func TestWriteAgentMCPConfigFile(t *testing.T) {
	root := t.TempDir()
	session := &domain.Sandbox{Summary: domain.SandboxSummary{WorkspacePath: filepath.Join(root, "workspace")}}
	config := &appconfig.Config{}
	mcps := map[string]compose.NormalizedMCPServerSpec{
		"filesystem": {Type: "local", Command: "npx", Args: []string{"-y", "server"}},
	}
	var pushedPath string
	var pushedContent []byte
	writer := func(_ context.Context, guestPath string, content []byte) error {
		pushedPath = guestPath
		pushedContent = content
		return nil
	}
	if err := WriteAgentMCPConfigFile(context.Background(), config, session, mcps, writer); err != nil {
		t.Fatalf("WriteAgentMCPConfigFile returned error: %v", err)
	}
	path := HostAgentMCPConfigPath(session)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), `"mcp_servers"`) || !strings.Contains(string(data), `"filesystem"`) {
		t.Fatalf("config = %q", string(data))
	}
	appconfig.ApplyDefaultGuestPaths(config)
	wantGuestPath := filepath.Join(config.GuestStateRoot, "agents", "mcp", "config.json")
	if pushedPath != wantGuestPath {
		t.Fatalf("pushed guest path = %q, want %q", pushedPath, wantGuestPath)
	}
	if string(pushedContent) != string(data) {
		t.Fatalf("pushed content = %q, want %q", pushedContent, data)
	}
	if err := WriteAgentMCPConfigFile(context.Background(), config, session, nil, nil); err != nil {
		t.Fatalf("WriteAgentMCPConfigFile remove returned error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected config file removed, stat err=%v", err)
	}
}

func TestWriteAgentMCPConfigFileClearsGuestOnlyWhenPreviouslyPushed(t *testing.T) {
	root := t.TempDir()
	mcps := map[string]compose.NormalizedMCPServerSpec{
		"filesystem": {Type: "local", Command: "npx", Args: []string{"-y", "server"}},
	}
	var pushCount int
	writer := func(context.Context, string, []byte) error {
		pushCount++
		return nil
	}

	// Never had any MCP servers configured: clearing must not push at all.
	fresh := &domain.Sandbox{Summary: domain.SandboxSummary{WorkspacePath: filepath.Join(root, "fresh")}}
	config := &appconfig.Config{}
	if err := WriteAgentMCPConfigFile(context.Background(), config, fresh, nil, writer); err != nil {
		t.Fatalf("WriteAgentMCPConfigFile (fresh) returned error: %v", err)
	}
	if pushCount != 0 {
		t.Fatalf("push count for a sandbox that never had MCP servers = %d, want 0", pushCount)
	}

	// Reused sandbox transitioning from having a server to having none: the
	// guest's stale copy must be cleared.
	reused := &domain.Sandbox{Summary: domain.SandboxSummary{WorkspacePath: filepath.Join(root, "reused")}}
	if err := WriteAgentMCPConfigFile(context.Background(), config, reused, mcps, writer); err != nil {
		t.Fatalf("WriteAgentMCPConfigFile (populate) returned error: %v", err)
	}
	pushCount = 0
	if err := WriteAgentMCPConfigFile(context.Background(), config, reused, nil, writer); err != nil {
		t.Fatalf("WriteAgentMCPConfigFile (clear) returned error: %v", err)
	}
	if pushCount != 1 {
		t.Fatalf("push count clearing a previously-populated config = %d, want 1", pushCount)
	}
}

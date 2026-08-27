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

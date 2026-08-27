package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	appconfig "agent-compose/pkg/config"

	"agent-compose/pkg/compose"
	domain "agent-compose/pkg/model"
)

type AgentMCPConfigPayload struct {
	MCPServers map[string]compose.NormalizedMCPServerSpec `json:"mcp_servers,omitempty"`
}

func HostAgentMCPConfigPath(session *domain.Sandbox) string {
	if session == nil || strings.TrimSpace(session.Summary.WorkspacePath) == "" {
		return ""
	}
	return filepath.Join(HostSandboxDir(session), "state", "agents", "mcp", "config.json")
}

func WriteAgentMCPConfigFile(ctx context.Context, config *appconfig.Config, session *domain.Sandbox, mcps map[string]compose.NormalizedMCPServerSpec, writeGuestFile GuestFileWriterFunc) error {
	hostPath := HostAgentMCPConfigPath(session)
	if hostPath == "" {
		if len(mcps) == 0 {
			return nil
		}
		return fmt.Errorf("sandbox workspace path is required to write agent mcp config")
	}
	if len(mcps) == 0 {
		if err := os.Remove(hostPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove agent mcp config file: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return fmt.Errorf("create agent mcp config dir: %w", err)
	}
	data, err := json.MarshalIndent(AgentMCPConfigPayload{MCPServers: mcps}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode agent mcp config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(hostPath, data, 0o644); err != nil {
		return fmt.Errorf("write agent mcp config file: %w", err)
	}
	if writeGuestFile != nil {
		appconfig.ApplyDefaultGuestPaths(config)
		guestPath := filepath.Join(config.GuestStateRoot, "agents", "mcp", "config.json")
		if err := writeGuestFile(ctx, guestPath, data); err != nil {
			return fmt.Errorf("push agent mcp config file to guest: %w", err)
		}
	}
	return nil
}

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
		_, statErr := os.Stat(hostPath)
		hadExisting := statErr == nil
		// docker/boxlite see the deletion for free through their shared mount.
		// A no-shared-mount guest (k8s) only finds out via this push, so a
		// sandbox reused across runs must still be told when every MCP server
		// has been removed - but only if this sandbox ever had a config
		// pushed in the first place, so a project with no MCP servers at all
		// doesn't pay for an exec round trip on every prepare call.
		//
		// Push to the guest before removing the host file: hostPath's
		// existence is what hadExisting is derived from on the next call, so
		// if the guest push failed and we'd already deleted hostPath, a
		// retry would see hadExisting=false and skip clearing the guest
		// forever, leaving it stuck with a stale config.
		if hadExisting && writeGuestFile != nil {
			appconfig.ApplyDefaultGuestPaths(config)
			guestPath := filepath.Join(config.GuestStateRoot, "agents", "mcp", "config.json")
			if err := writeGuestFile(ctx, guestPath, nil); err != nil {
				return fmt.Errorf("clear agent mcp config file on guest: %w", err)
			}
		}
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

package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"agent-compose/pkg/workspaces"
)

func standaloneWorkspaceIDs(agents []convertedStandaloneAgent) []string {
	unique := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		if id := strings.TrimSpace(agent.definition.workspaceID); id != "" {
			unique[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func loadLegacyProjectWorkspaces(ctx context.Context, db *sql.DB, ids []string) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		var name, workspaceType, configJSON string
		if err := db.QueryRowContext(ctx, `SELECT name,type,config_json FROM workspace_config WHERE id=?`, id).Scan(&name, &workspaceType, &configJSON); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("standalone agent workspace %s is not defined", id)
			}
			return nil, fmt.Errorf("load standalone agent workspace %s: %w", id, err)
		}
		workspace, err := legacyProjectWorkspace(id, name, workspaceType, configJSON)
		if err != nil {
			return nil, err
		}
		result = append(result, workspace)
	}
	return result, nil
}

func legacyProjectWorkspace(id, name, workspaceType, configJSON string) (map[string]any, error) {
	result := map[string]any{"key": id, "name": name}
	switch strings.ToLower(strings.TrimSpace(workspaceType)) {
	case "file":
		path, err := workspaces.FileWorkspaceContentRelRoot(id)
		if err != nil {
			return nil, fmt.Errorf("convert standalone file workspace %s: %w", id, err)
		}
		result["provider"] = "file"
		result["path"] = path
	case "git":
		config, err := workspaces.DecodeGitWorkspaceConfig(configJSON)
		if err != nil {
			return nil, fmt.Errorf("decode standalone git workspace %s: %w", id, err)
		}
		result["provider"] = "git"
		result["url"] = config.URL
		if config.Ref != "" {
			result["ref"] = config.Ref
		}
		if config.Format != "" {
			result["format"] = config.Format
		}
		if config.Username != "" {
			result["username"] = config.Username
		}
		if config.Password != "" {
			result["password"] = config.Password
		}
		if config.Token != "" {
			result["token"] = config.Token
		}
		if config.Target != "" {
			result["target"] = config.Target
		}
	default:
		return nil, fmt.Errorf("standalone workspace %s has unsupported type %q", id, workspaceType)
	}
	return result, nil
}

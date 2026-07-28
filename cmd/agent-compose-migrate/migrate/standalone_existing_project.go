package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func loadExistingLegacyProjectAgents(ctx context.Context, db *sql.DB, projectID string) (map[string]legacyAgentDefinition, error) {
	rows, err := db.QueryContext(ctx, `SELECT definition.id,definition.name,definition.description,definition.enabled,definition.deleted_at,
		definition.provider,definition.model,definition.system_prompt,definition.driver,definition.guest_image,definition.workspace_id,
		definition.env_json,definition.volumes_json,definition.config_json,definition.capset_ids,definition.skills,
		definition.created_at,definition.updated_at,agent.agent_name
		FROM project_agent AS agent JOIN agent_definition AS definition ON definition.id=agent.managed_agent_id
		WHERE agent.project_id=? ORDER BY agent.agent_name`, projectID)
	if err != nil {
		return nil, fmt.Errorf("load existing legacy project agents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make(map[string]legacyAgentDefinition)
	for rows.Next() {
		var item legacyAgentDefinition
		var name string
		if err := rows.Scan(&item.id, &item.name, &item.description, &item.enabled, &item.deletedAt,
			&item.provider, &item.model, &item.systemPrompt, &item.driver, &item.image, &item.workspaceID,
			&item.envJSON, &item.volumesJSON, &item.configJSON, &item.capsetIDs, &item.skills,
			&item.createdAt, &item.updatedAt, &name); err != nil {
			return nil, fmt.Errorf("scan existing legacy project agent: %w", err)
		}
		result[strings.TrimSpace(name)] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate existing legacy project agents: %w", err)
	}
	return result, nil
}

func equivalentLegacyAgentDefinitions(left, right legacyAgentDefinition) bool {
	return left.description == right.description &&
		left.enabled == right.enabled &&
		left.deletedAt == right.deletedAt &&
		left.provider == right.provider &&
		left.model == right.model &&
		left.systemPrompt == right.systemPrompt &&
		left.driver == right.driver &&
		left.image == right.image &&
		left.workspaceID == right.workspaceID &&
		equivalentLegacyJSON(left.envJSON, right.envJSON) &&
		equivalentLegacyJSON(left.volumesJSON, right.volumesJSON) &&
		equivalentLegacyJSON(left.configJSON, right.configJSON) &&
		equivalentLegacyJSON(left.capsetIDs, right.capsetIDs) &&
		equivalentLegacyJSON(left.skills, right.skills)
}

func equivalentLegacyJSON(left, right string) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal([]byte(left), &leftValue) != nil || json.Unmarshal([]byte(right), &rightValue) != nil {
		return strings.TrimSpace(left) == strings.TrimSpace(right)
	}
	return equivalentLegacyJSONValues(leftValue, rightValue)
}

func equivalentLegacyJSONValues(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func loadLegacyProjectSpec(ctx context.Context, db *sql.DB, projectID string) (map[string]any, []map[string]any, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT revision.spec_json FROM project
		JOIN project_revision AS revision ON revision.project_id=project.id AND revision.revision=project.current_revision
		WHERE project.id=?`, projectID).Scan(&raw)
	if err == sql.ErrNoRows {
		return map[string]any{"name": legacyDefaultProjectName}, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load existing legacy project revision: %w", err)
	}
	var spec map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&spec); err != nil {
		return nil, nil, fmt.Errorf("decode existing legacy project revision: %w", err)
	}
	agents, err := projectedRevisionAgents(ctx, db, projectID)
	if err != nil {
		return nil, nil, err
	}
	return spec, agents, nil
}

func mergeLegacyProjectAgents(existing, additions []map[string]any) ([]map[string]any, error) {
	byName := make(map[string]map[string]any, len(existing)+len(additions))
	for _, item := range existing {
		name, _ := item["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("existing legacy project agent has no name")
		}
		byName[name] = item
	}
	for _, item := range additions {
		name, _ := item["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("converted standalone agent has no name")
		}
		byName[name] = item
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]map[string]any, 0, len(names))
	for _, name := range names {
		result = append(result, byName[name])
	}
	return result, nil
}

func mergeLegacyNamedSpecItems(spec map[string]any, key string, additions []map[string]any) error {
	if len(additions) == 0 {
		return nil
	}
	byName := make(map[string]any)
	if current, ok := spec[key].([]any); ok {
		for _, value := range current {
			item, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("existing legacy project %s entry is not an object", key)
			}
			name, _ := item["name"].(string)
			byName[strings.TrimSpace(name)] = item
		}
	}
	for _, item := range additions {
		name, _ := item["name"].(string)
		name = strings.TrimSpace(name)
		if existing, ok := byName[name]; ok && !equivalentLegacyJSONValues(existing, item) {
			return fmt.Errorf("standalone conversion conflicts with existing legacy project %s %s", key, name)
		}
		byName[name] = item
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	merged := make([]any, 0, len(names))
	for _, name := range names {
		merged = append(merged, byName[name])
	}
	spec[key] = merged
	return nil
}

package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-compose/pkg/identity"
	domain "agent-compose/pkg/model"
)

const legacyDefaultProjectName = "legacy-v1-default"

type legacyAgentDefinition struct {
	id, name, description, provider, model, systemPrompt, driver, image string
	workspaceID, envJSON, volumesJSON, configJSON, capsetIDs, skills    string
	enabled, deletedAt, createdAt, updatedAt                            int64
}

type legacySchedulerDefinition struct {
	id, name, description, runtime, script, workspaceID, agentID  string
	driver, image, defaultAgent, sandboxPolicy, concurrencyPolicy string
	capsetIDs, envJSON, volumesJSON, lastError                    string
	enabled, createdAt, updatedAt                                 int64
}

type convertedStandaloneAgent struct {
	definition legacyAgentDefinition
	name       string
	nativeID   string
	scheduler  *legacySchedulerDefinition
}

func convertStandaloneV1(ctx context.Context, db *sql.DB, targetRoot string) ([]string, error) {
	agents, err := loadStandaloneAgents(ctx, db)
	if err != nil {
		return nil, err
	}
	schedulers, err := loadStandaloneSchedulers(ctx, db)
	if err != nil {
		return nil, err
	}
	deletedAgentIDs, err := loadDeletedStandaloneAgentIDs(ctx, db)
	if err != nil {
		return nil, err
	}
	if len(agents) == 0 && len(schedulers) == 0 && len(deletedAgentIDs) == 0 {
		return nil, nil
	}
	if len(agents) == 0 && len(schedulers) == 0 {
		return nil, removeOnlyDeletedStandaloneAgents(ctx, db, deletedAgentIDs)
	}

	projectID := identity.NewID(identity.ResourceProject, legacyDefaultProjectName, "")
	revision, err := nextLegacyRevision(ctx, db, projectID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Unix()
	converted, err := planStandaloneAgents(projectID, agents, schedulers, now)
	if err != nil {
		return nil, err
	}
	specWorkspaces, err := loadLegacyProjectWorkspaces(ctx, db, standaloneWorkspaceIDs(converted))
	if err != nil {
		return nil, err
	}
	if err := saveStandaloneConversion(ctx, db, targetRoot, projectID, revision, now, agents, deletedAgentIDs, converted, specWorkspaces); err != nil {
		return nil, err
	}
	return []string{fmt.Sprintf("converted %d standalone agents and %d standalone schedulers into project %s", len(agents), len(schedulers), legacyDefaultProjectName)}, nil
}

func planStandaloneAgents(projectID string, agents []legacyAgentDefinition, schedulers []legacySchedulerDefinition, now int64) ([]convertedStandaloneAgent, error) {
	usedNames := make(map[string]struct{}, len(agents)+len(schedulers))
	converted := make([]convertedStandaloneAgent, 0, len(agents)+len(schedulers))
	agentIndexes := make(map[string]int, len(agents))
	boundSchedulerCounts := make(map[string]int, len(schedulers))
	for _, scheduler := range schedulers {
		boundSchedulerCounts[strings.TrimSpace(scheduler.agentID)]++
	}
	for _, agent := range agents {
		agentName := uniqueLegacyName(agent.name, agent.id, "agent", usedNames)
		nativeID, stableErr := domain.StableProjectAgentID(projectID, agentName)
		if stableErr != nil {
			return nil, fmt.Errorf("derive standalone agent identity %s: %w", agent.id, stableErr)
		}
		agentIndexes[agent.id] = len(converted)
		converted = append(converted, convertedStandaloneAgent{definition: agent, name: agentName, nativeID: nativeID})
	}
	var splitSchedulers []legacySchedulerDefinition
	for _, scheduler := range schedulers {
		index, found := agentIndexes[strings.TrimSpace(scheduler.agentID)]
		if found && boundSchedulerCounts[strings.TrimSpace(scheduler.agentID)] == 1 && schedulerCanReuseStandaloneAgent(converted[index].definition, scheduler) {
			schedulerCopy := scheduler
			converted[index].scheduler = &schedulerCopy
			continue
		}
		splitSchedulers = append(splitSchedulers, scheduler)
	}
	for _, scheduler := range splitSchedulers {
		agentName := uniqueLegacyName(scheduler.name, scheduler.id, "scheduler", usedNames)
		nativeID, stableErr := domain.StableProjectAgentID(projectID, agentName)
		if stableErr != nil {
			return nil, fmt.Errorf("derive standalone scheduler agent identity %s: %w", scheduler.id, stableErr)
		}
		converted = append(converted, convertedStandaloneAgent{
			definition: legacyAgentFromScheduler(scheduler, nativeID, agentName, now),
			name:       agentName,
			nativeID:   nativeID,
			scheduler:  legacySchedulerPointer(scheduler),
		})
	}
	return converted, nil
}

func saveStandaloneConversion(ctx context.Context, db *sql.DB, targetRoot, projectID string, revision, now int64, agents []legacyAgentDefinition, deletedAgentIDs []string, converted []convertedStandaloneAgent, specWorkspaces []map[string]any) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin standalone conversion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO project(
		id, name, short_id, source_path, source_json, current_revision, spec_hash, created_at, updated_at, removed_at
	) VALUES(?, ?, ?, ?, '{}', 0, '', ?, ?, 0)`, projectID, legacyDefaultProjectName, shortLegacyID(projectID), targetRoot, now, now); err != nil {
		return fmt.Errorf("create standalone project: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project SET source_path=? WHERE id=? AND trim(source_path)=''`, targetRoot, projectID); err != nil {
		return fmt.Errorf("set standalone project source path: %w", err)
	}
	if err := removeDeletedStandaloneAgents(ctx, tx, deletedAgentIDs); err != nil {
		return err
	}
	if err := remapStandaloneAgentIdentities(ctx, tx, converted[:len(agents)]); err != nil {
		return err
	}

	specAgents := make([]map[string]any, 0, len(converted))
	for index := range converted {
		item := &converted[index]
		if index >= len(agents) {
			if err := insertSyntheticAgentDefinition(ctx, tx, projectID, revision, item.name, item.definition); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `UPDATE agent_definition SET managed_project_id=?, managed_project_revision=?, managed_agent_name=? WHERE id=?`, projectID, revision, item.name, item.nativeID); err != nil {
			return fmt.Errorf("attach standalone agent %s: %w", item.definition.id, err)
		}
		var schedulerJSON map[string]any
		if item.scheduler != nil {
			schedulerJSON = legacySchedulerJSON(*item.scheduler)
		}
		specAgents = append(specAgents, legacyAgentJSON(item.definition, item.name, schedulerJSON))
		if err := insertLegacyProjectAgent(ctx, tx, projectID, revision, item.name, item.nativeID, item.definition, item.scheduler != nil); err != nil {
			return err
		}
		if item.scheduler == nil {
			continue
		}
		scheduler := *item.scheduler
		schedulerID, stableErr := domain.StableProjectSchedulerID(projectID, item.name, "")
		if stableErr != nil {
			return fmt.Errorf("derive standalone scheduler identity %s: %w", scheduler.id, stableErr)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_scheduler(
			id, short_id, project_id, scheduler_id, agent_name, managed_loader_id, revision, enabled, trigger_count, spec_json, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, (SELECT COUNT(*) FROM loader_trigger WHERE loader_id=?), ?, ?, ?)`,
			schedulerID, shortLegacyID(schedulerID), projectID, schedulerID, item.name, scheduler.id, revision,
			scheduler.enabled, scheduler.id, mustJSON(schedulerJSON), scheduler.createdAt, scheduler.updatedAt); err != nil {
			return fmt.Errorf("project standalone scheduler %s: %w", scheduler.id, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE loader SET managed_project_id=?, managed_project_revision=?, managed_agent_name=?, managed_scheduler_id=? WHERE id=?`, projectID, revision, item.name, schedulerID, scheduler.id); err != nil {
			return fmt.Errorf("attach standalone scheduler %s: %w", scheduler.id, err)
		}
	}

	spec := map[string]any{"name": legacyDefaultProjectName, "agents": specAgents}
	if len(specWorkspaces) > 0 {
		spec["workspaces"] = specWorkspaces
	}
	specData, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("marshal standalone project revision: %w", err)
	}
	specSum := sha256.Sum256(specData)
	specHash := hex.EncodeToString(specSum[:])
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_revision(project_id, revision, spec_hash, spec_json, created_at) VALUES(?, ?, ?, ?, ?)`, projectID, revision, specHash, string(specData), now); err != nil {
		return fmt.Errorf("save standalone project revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project SET current_revision=?, spec_hash=?, updated_at=? WHERE id=?`, revision, specHash, now, projectID); err != nil {
		return fmt.Errorf("activate standalone project revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit standalone conversion: %w", err)
	}
	return nil
}

func legacySchedulerPointer(scheduler legacySchedulerDefinition) *legacySchedulerDefinition {
	return &scheduler
}

func loadStandaloneAgents(ctx context.Context, db *sql.DB) ([]legacyAgentDefinition, error) {
	rows, err := db.QueryContext(ctx, `SELECT d.id,d.name,d.description,d.enabled,d.deleted_at,d.provider,d.model,d.system_prompt,d.driver,d.guest_image,d.workspace_id,d.env_json,d.volumes_json,d.config_json,d.capset_ids,d.skills,d.created_at,d.updated_at
		FROM agent_definition d LEFT JOIN project_agent a ON a.managed_agent_id=d.id WHERE a.id IS NULL AND d.deleted_at=0 ORDER BY d.id`)
	if err != nil {
		return nil, fmt.Errorf("query standalone agents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var result []legacyAgentDefinition
	for rows.Next() {
		var item legacyAgentDefinition
		if err := rows.Scan(&item.id, &item.name, &item.description, &item.enabled, &item.deletedAt, &item.provider, &item.model, &item.systemPrompt, &item.driver, &item.image, &item.workspaceID, &item.envJSON, &item.volumesJSON, &item.configJSON, &item.capsetIDs, &item.skills, &item.createdAt, &item.updatedAt); err != nil {
			return nil, fmt.Errorf("scan standalone agent: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func loadStandaloneSchedulers(ctx context.Context, db *sql.DB) ([]legacySchedulerDefinition, error) {
	rows, err := db.QueryContext(ctx, `SELECT l.id,l.name,l.description,l.runtime,l.script,l.workspace_id,l.agent_id,l.driver,l.guest_image,l.default_agent,l.sandbox_policy,l.concurrency_policy,l.capset_ids,l.env_json,l.volumes_json,l.enabled,l.last_error,l.created_at,l.updated_at
		FROM loader l LEFT JOIN project_scheduler s ON s.managed_loader_id=l.id WHERE s.id IS NULL ORDER BY l.id`)
	if err != nil {
		return nil, fmt.Errorf("query standalone schedulers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var result []legacySchedulerDefinition
	for rows.Next() {
		var item legacySchedulerDefinition
		if err := rows.Scan(&item.id, &item.name, &item.description, &item.runtime, &item.script, &item.workspaceID, &item.agentID, &item.driver, &item.image, &item.defaultAgent, &item.sandboxPolicy, &item.concurrencyPolicy, &item.capsetIDs, &item.envJSON, &item.volumesJSON, &item.enabled, &item.lastError, &item.createdAt, &item.updatedAt); err != nil {
			return nil, fmt.Errorf("scan standalone scheduler: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func insertLegacyProjectAgent(ctx context.Context, tx *sql.Tx, projectID string, revision int64, agentName, nativeID string, agent legacyAgentDefinition, schedulerEnabled bool) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_agent(id,name,short_id,project_id,agent_name,managed_agent_id,revision,provider,model,image,driver,scheduler_enabled,spec_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, nativeID, agent.name, shortLegacyID(nativeID), projectID, agentName, nativeID, revision, agent.provider, agent.model, agent.image, agent.driver, schedulerEnabled, mustJSON(legacyAgentJSON(agent, agentName, nil)), agent.createdAt, agent.updatedAt); err != nil {
		return fmt.Errorf("project standalone agent %s: %w", agent.id, err)
	}
	return nil
}

func insertSyntheticAgentDefinition(ctx context.Context, tx *sql.Tx, projectID string, revision int64, agentName string, agent legacyAgentDefinition) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO agent_definition(id,name,description,enabled,deleted_at,provider,model,system_prompt,driver,guest_image,workspace_id,env_json,volumes_json,config_json,capset_ids,skills,managed_project_id,managed_project_revision,managed_agent_name,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, agent.id, agent.name, agent.description, agent.enabled, agent.deletedAt, agent.provider, agent.model, agent.systemPrompt, agent.driver, agent.image, agent.workspaceID, agent.envJSON, agent.volumesJSON, agent.configJSON, agent.capsetIDs, agent.skills, projectID, revision, agentName, agent.createdAt, agent.updatedAt)
	if err != nil {
		return fmt.Errorf("create scheduler agent definition %s: %w", agent.id, err)
	}
	return nil
}

func legacyAgentFromScheduler(item legacySchedulerDefinition, id, name string, now int64) legacyAgentDefinition {
	return legacyAgentDefinition{id: id, name: name, description: item.description, provider: item.defaultAgent, driver: item.driver, image: item.image, workspaceID: item.workspaceID, envJSON: item.envJSON, volumesJSON: item.volumesJSON, configJSON: "{}", capsetIDs: item.capsetIDs, skills: "[]", enabled: 1, createdAt: firstLegacyTime(item.createdAt, now), updatedAt: firstLegacyTime(item.updatedAt, now)}
}

func legacyAgentJSON(item legacyAgentDefinition, name string, scheduler map[string]any) map[string]any {
	result := map[string]any{"name": name, "enabled": item.enabled != 0, "display_name": item.name, "description": item.description, "provider": item.provider, "model": item.model, "system_prompt": item.systemPrompt, "image": item.image, "env": legacyEnvList(item.envJSON), "capset_ids": legacyJSONValue(item.capsetIDs, []any{}), "skills": legacyJSONValue(item.skills, []any{}), "volumes": legacyJSONValue(item.volumesJSON, []any{})}
	if item.driver != "" {
		result["driver"] = map[string]any{"name": item.driver}
	}
	if item.workspaceID != "" {
		result["workspace"] = map[string]any{"name": item.workspaceID}
	}
	var config map[string]any
	if json.Unmarshal([]byte(item.configJSON), &config) == nil {
		for _, key := range []string{"jupyter", "mcp_servers"} {
			if value, ok := config[key]; ok {
				result[key] = value
			}
		}
	}
	if scheduler != nil {
		result["scheduler"] = scheduler
	}
	return result
}

func legacySchedulerJSON(item legacySchedulerDefinition) map[string]any {
	return map[string]any{"enabled": item.enabled != 0, "sandbox_policy": item.sandboxPolicy, "concurrency_policy": item.concurrencyPolicy, "display_name": item.name, "description": item.description, "script": item.script}
}

func legacyEnvList(raw string) []map[string]any {
	var items []map[string]any
	if json.Unmarshal([]byte(raw), &items) != nil {
		return nil
	}
	return items
}

func legacyJSONValue(raw string, fallback any) any {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return fallback
	}
	return value
}

func uniqueLegacyName(preferred, id, prefix string, used map[string]struct{}) string {
	base := strings.ToLower(strings.TrimSpace(preferred))
	var cleaned strings.Builder
	for _, char := range base {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			cleaned.WriteRune(char)
		}
	}
	base = strings.Trim(cleaned.String(), "-_")
	if base == "" {
		base = prefix
	}
	if base[0] < 'a' || base[0] > 'z' {
		base = prefix + "-" + base
	}
	candidate := base
	if _, exists := used[candidate]; exists {
		sum := sha256.Sum256([]byte(id))
		candidate = base + "-" + hex.EncodeToString(sum[:6])
	}
	used[candidate] = struct{}{}
	return candidate
}

func schedulerCanReuseStandaloneAgent(agent legacyAgentDefinition, scheduler legacySchedulerDefinition) bool {
	return compatibleLegacyOverride(scheduler.workspaceID, agent.workspaceID) &&
		compatibleLegacyOverride(scheduler.driver, agent.driver) &&
		compatibleLegacyOverride(scheduler.image, agent.image) &&
		compatibleLegacyJSONOverride(scheduler.envJSON, agent.envJSON) &&
		compatibleLegacyJSONOverride(scheduler.volumesJSON, agent.volumesJSON) &&
		compatibleLegacyJSONOverride(scheduler.capsetIDs, agent.capsetIDs)
}

func compatibleLegacyOverride(override, base string) bool {
	override = strings.TrimSpace(override)
	return override == "" || override == strings.TrimSpace(base)
}

func compatibleLegacyJSONOverride(override, base string) bool {
	var overrideValue any
	if err := json.Unmarshal([]byte(strings.TrimSpace(override)), &overrideValue); err != nil {
		return strings.TrimSpace(override) == ""
	}
	if values, ok := overrideValue.([]any); ok && len(values) == 0 {
		return true
	}
	var baseValue any
	return json.Unmarshal([]byte(strings.TrimSpace(base)), &baseValue) == nil && mustJSON(overrideValue) == mustJSON(baseValue)
}

func nextLegacyRevision(ctx context.Context, db *sql.DB, projectID string) (int64, error) {
	var revision int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM project_revision WHERE project_id=?`, projectID).Scan(&revision); err != nil {
		return 0, fmt.Errorf("select standalone project revision: %w", err)
	}
	return revision, nil
}

func shortLegacyID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func firstLegacyTime(value, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

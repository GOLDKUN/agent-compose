package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type journal struct {
	Mode              string                             `json:"mode,omitempty"`
	SourceFingerprint string                             `json:"source_fingerprint"`
	SourceVersion     int64                              `json:"source_version,omitempty"`
	RuntimeRoot       string                             `json:"runtime_root"`
	Stage             string                             `json:"stage"`
	Complete          bool                               `json:"complete"`
	SchedulerIDs      map[string]string                  `json:"scheduler_ids"`
	AgentIDs          map[string]standaloneAgentIdentity `json:"agent_ids,omitempty"`
	Warnings          []string                           `json:"warnings,omitempty"`
}

type standaloneAgentIdentity struct {
	NativeID  string `json:"native_id"`
	ProjectID string `json:"project_id"`
	AgentName string `json:"agent_name"`
}

func prepareTarget(target, fingerprint, runtimeRoot string) (journal, error) {
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(target, 0o700); err != nil {
			return journal{}, fmt.Errorf("create target: %w", err)
		}
		return journal{SourceFingerprint: fingerprint, RuntimeRoot: runtimeRoot}, nil
	}
	if err != nil {
		return journal{}, fmt.Errorf("inspect target: %w", err)
	}
	if !info.IsDir() {
		return journal{}, fmt.Errorf("target must be a directory")
	}
	data, err := os.ReadFile(filepath.Join(target, journalName))
	if err != nil {
		return journal{}, fmt.Errorf("target exists without a resumable migration journal")
	}
	var state journal
	if err := json.Unmarshal(data, &state); err != nil {
		return journal{}, fmt.Errorf("decode target migration journal: %w", err)
	}
	if state.SourceFingerprint != fingerprint {
		return journal{}, fmt.Errorf("source fingerprint changed since the target migration started")
	}
	// Journals created before runtime-root became explicit used the target path.
	if state.RuntimeRoot == "" {
		state.RuntimeRoot = target
	}
	if filepath.Clean(state.RuntimeRoot) != filepath.Clean(runtimeRoot) {
		return journal{}, fmt.Errorf("runtime root changed since the target migration started")
	}
	if err := validateJournalState(state); err != nil {
		return journal{}, err
	}
	return state, nil
}

func validateJournalState(state journal) error {
	if !filepath.IsAbs(state.RuntimeRoot) {
		return fmt.Errorf("migration journal has invalid runtime root %q", state.RuntimeRoot)
	}
	if state.Mode == "in_place" {
		switch state.Stage {
		case "database", inPlaceStagePrepared, inPlaceStageLayout, inPlaceStageSwitch:
			if state.Complete {
				return fmt.Errorf("in-place %s journal cannot be marked complete", state.Stage)
			}
		case "complete":
			if !state.Complete {
				return fmt.Errorf("complete migration journal is not sealed")
			}
		default:
			return fmt.Errorf("in-place migration journal has unknown stage %q", state.Stage)
		}
		return nil
	}
	switch state.Stage {
	case "database":
		if state.Complete {
			return fmt.Errorf("database migration journal cannot be marked complete")
		}
	case "files":
		if state.Complete {
			return fmt.Errorf("file migration journal cannot be marked complete")
		}
		if state.SchedulerIDs == nil {
			return fmt.Errorf("file migration journal is missing scheduler identity mappings")
		}
	case "complete":
		if !state.Complete {
			return fmt.Errorf("complete migration journal is not sealed")
		}
		if state.SchedulerIDs == nil {
			return fmt.Errorf("complete migration journal is missing scheduler identity mappings")
		}
	default:
		return fmt.Errorf("migration journal has unknown stage %q", state.Stage)
	}
	return nil
}

func readJournal(root string) (journal, error) {
	path := filepath.Join(root, journalName)
	info, err := os.Lstat(path)
	if err != nil {
		return journal{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return journal{}, fmt.Errorf("migration journal must be a regular file, not a symlink")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return journal{}, err
	}
	var state journal
	if err := json.Unmarshal(data, &state); err != nil {
		return journal{}, fmt.Errorf("decode target migration journal: %w", err)
	}
	if err := validateJournalState(state); err != nil {
		return journal{}, err
	}
	return state, nil
}

func writeJournal(target string, state journal) error {
	stateData, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temporary := filepath.Join(target, journalName+".tmp")
	if err := os.WriteFile(temporary, append(stateData, '\n'), 0o600); err != nil {
		return fmt.Errorf("write migration journal: %w", err)
	}
	if err := os.Rename(temporary, filepath.Join(target, journalName)); err != nil {
		return fmt.Errorf("seal migration journal: %w", err)
	}
	return nil
}

func cloneSchedulerIDs(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for legacyID, schedulerID := range source {
		result[legacyID] = schedulerID
	}
	return result
}

func cloneStandaloneAgentIdentities(source map[string]standaloneAgentIdentity) map[string]standaloneAgentIdentity {
	if source == nil {
		return nil
	}
	result := make(map[string]standaloneAgentIdentity, len(source))
	for legacyID, identity := range source {
		result[legacyID] = identity
	}
	return result
}

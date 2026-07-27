package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func copyAuthoritativeFiles(source, target, runtimeRoot string, schedulerIDs map[string]string, agentIDs map[string]standaloneAgentIdentity) (int, int64, error) {
	var files int
	var bytes int64
	mappedSources := make(map[string]string)
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		if rel == inPlaceBackupName && entry.IsDir() {
			return filepath.SkipDir
		}
		if rel == databaseName || rel == databaseName+"-wal" || rel == databaseName+"-shm" || rel == journalName {
			return nil
		}
		mappedRel := migratedDataRootPath(rel, schedulerIDs)
		if previous, exists := mappedSources[mappedRel]; exists && previous != rel {
			return fmt.Errorf("legacy paths %s and %s both map to %s", previous, rel, mappedRel)
		}
		mappedSources[mappedRel] = rel
		destination := filepath.Join(target, mappedRel)
		if entry.Type()&os.ModeSymlink != 0 {
			if err := validateMigratableSourceSymlink(rel); err != nil {
				return err
			}
			if err := copyMigratedSymlink(path, destination, mappedRel, source, target, runtimeRoot, schedulerIDs); err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			files++
			bytes += info.Size()
			return nil
		}
		if err := ensureSafeTargetPath(target, destination); err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refuse non-regular source file: %s", rel)
		}
		if err := copyMigratedFile(path, destination, mappedRel, source, runtimeRoot, schedulerIDs, agentIDs, info.Mode().Perm()); err != nil {
			return err
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
}

func inspectAuthoritativeFiles(source, runtimeRoot string, schedulerIDs map[string]string, agentIDs map[string]standaloneAgentIdentity) (int, int64, error) {
	var files int
	var bytes int64
	mappedSources := make(map[string]string)
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		if rel == inPlaceBackupName && entry.IsDir() {
			return filepath.SkipDir
		}
		if rel == databaseName || rel == databaseName+"-wal" || rel == databaseName+"-shm" || rel == journalName {
			return nil
		}
		mappedRel := migratedDataRootPath(rel, schedulerIDs)
		if previous, exists := mappedSources[mappedRel]; exists && previous != rel {
			return fmt.Errorf("legacy paths %s and %s both map to %s", previous, rel, mappedRel)
		}
		mappedSources[mappedRel] = rel
		if entry.Type()&os.ModeSymlink != 0 {
			if err := validateMigratableSourceSymlink(rel); err != nil {
				return err
			}
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if _, _, err := migratedSymlinkTarget(path, mappedRel, target, source, runtimeRoot, schedulerIDs); err != nil {
				return fmt.Errorf("rewrite source symlink %s: %w", rel, err)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			files++
			bytes += info.Size()
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refuse non-regular source file: %s", rel)
		}
		if _, _, err := rewriteMigratedJSON(path, mappedRel, source, runtimeRoot, schedulerIDs, agentIDs); err != nil {
			return err
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
}

func copyMigratedFile(sourcePath, destination, mappedRel, sourceRoot, runtimeRoot string, schedulerIDs map[string]string, agentIDs map[string]standaloneAgentIdentity, mode fs.FileMode) error {
	data, handled, err := rewriteMigratedJSON(sourcePath, mappedRel, sourceRoot, runtimeRoot, schedulerIDs, agentIDs)
	if err != nil {
		return err
	}
	if !handled {
		return copyFile(sourcePath, destination, mode)
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, writeErr := out.Write(data)
	closeErr := out.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func rewriteMigratedJSON(sourcePath, mappedRel, sourceRoot, runtimeRoot string, schedulerIDs map[string]string, agentIDs map[string]standaloneAgentIdentity) ([]byte, bool, error) {
	parts := strings.Split(filepath.ToSlash(mappedRel), "/")
	isMetadata := len(parts) >= 3 && parts[0] == "sandboxes" && parts[len(parts)-1] == "metadata.json"
	isManifest := len(parts) >= 4 && parts[0] == "sandboxes" && parts[len(parts)-2] == "vm" && parts[len(parts)-1] == "mount-manifest.json"
	isLifecycle := len(parts) == 3 && parts[0] == "sandboxes" && parts[1] == ".lifecycle" && strings.HasSuffix(parts[2], ".json")
	if !isMetadata && !isManifest && !isLifecycle {
		return nil, false, nil
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, true, err
	}
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, true, fmt.Errorf("decode migratable JSON %s: %w", mappedRel, err)
	}
	rewrite := func(container map[string]any, field string) error {
		value, ok := container[field].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return nil
		}
		migrated, inside, err := migratedStoredPath(value, sourceRoot, runtimeRoot, schedulerIDs)
		if err != nil {
			return err
		}
		if inside {
			container[field] = migrated
		}
		return nil
	}
	if isMetadata {
		if summary, ok := document["summary"].(map[string]any); ok {
			if err := rewrite(summary, "workspace_path"); err != nil {
				return nil, true, fmt.Errorf("rewrite metadata workspace path: %w", err)
			}
			if err := rewriteStandaloneAgentTags(summary, agentIDs); err != nil {
				return nil, true, fmt.Errorf("rewrite metadata agent identity: %w", err)
			}
		}
		if mounts, ok := document["volume_mounts"].([]any); ok {
			for _, item := range mounts {
				if mount, ok := item.(map[string]any); ok {
					if err := rewrite(mount, "host_path"); err != nil {
						return nil, true, fmt.Errorf("rewrite metadata volume mount: %w", err)
					}
				}
			}
		}
	}
	if isManifest {
		if mounts, ok := document["mounts"].([]any); ok {
			for _, item := range mounts {
				if mount, ok := item.(map[string]any); ok {
					if err := rewrite(mount, "hostPath"); err != nil {
						return nil, true, fmt.Errorf("rewrite runtime mount manifest: %w", err)
					}
				}
			}
		}
	}
	if isLifecycle {
		sandboxID := strings.TrimSuffix(parts[2], ".json")
		if err := rewriteLifecyclePaths(document, sandboxID, sourceRoot, runtimeRoot, schedulerIDs); err != nil {
			return nil, true, err
		}
	}
	rewritten, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, true, fmt.Errorf("encode migratable JSON %s: %w", mappedRel, err)
	}
	return append(rewritten, '\n'), true, nil
}

func rewriteStandaloneAgentTags(summary map[string]any, agentIDs map[string]standaloneAgentIdentity) error {
	tags, ok := summary["tags"].([]any)
	if !ok {
		return nil
	}
	legacyAgentID := ""
	for _, item := range tags {
		tag, ok := item.(map[string]any)
		if !ok || tag["name"] != "agent_id" {
			continue
		}
		legacyAgentID, _ = tag["value"].(string)
		legacyAgentID = strings.TrimSpace(legacyAgentID)
		if legacyAgentID != "" {
			break
		}
	}
	identity, exists := agentIDs[legacyAgentID]
	if !exists {
		return nil
	}
	if strings.TrimSpace(identity.NativeID) == "" || strings.TrimSpace(identity.ProjectID) == "" || strings.TrimSpace(identity.AgentName) == "" {
		return fmt.Errorf("standalone agent %s has an incomplete identity mapping", legacyAgentID)
	}
	desired := map[string]string{
		"agent_id":   identity.NativeID,
		"agent_name": identity.AgentName,
		"agent":      identity.AgentName,
		"project":    identity.ProjectID,
		"project_id": identity.ProjectID,
	}
	found := make(map[string]bool, len(desired))
	for _, item := range tags {
		tag, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := tag["name"].(string)
		value, known := desired[strings.TrimSpace(name)]
		if !known {
			continue
		}
		tag["value"] = value
		found[strings.TrimSpace(name)] = true
	}
	for _, name := range []string{"agent_id", "agent_name", "agent", "project", "project_id"} {
		if !found[name] {
			tags = append(tags, map[string]any{"name": name, "value": desired[name]})
		}
	}
	summary["tags"] = tags
	return nil
}

func migratedDataRootPath(rel string, schedulerIDs map[string]string) string {
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	if len(parts) == 0 {
		return rel
	}
	switch parts[0] {
	case "sessions":
		parts[0] = "sandboxes"
	case "loaders":
		parts[0] = "schedulers"
		if len(parts) > 1 {
			if schedulerID := strings.TrimSpace(schedulerIDs[parts[1]]); schedulerID != "" {
				parts[1] = schedulerID
			}
		}
	}
	return filepath.Join(parts...)
}

func validateStoppedLegacySandboxes(source string) error {
	for _, rootName := range []string{"sessions", "sandboxes"} {
		root := filepath.Join(source, rootName)
		if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("inspect legacy sandbox root %s: %w", rootName, err)
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || entry.Name() != "metadata.json" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read sandbox metadata %s: %w", path, err)
			}
			var metadata struct {
				Summary struct {
					ID       string `json:"id"`
					VMStatus string `json:"vm_status"`
				} `json:"summary"`
			}
			if err := json.Unmarshal(data, &metadata); err != nil {
				return fmt.Errorf("decode sandbox metadata %s: %w", path, err)
			}
			if strings.EqualFold(strings.TrimSpace(metadata.Summary.VMStatus), "running") {
				return fmt.Errorf("sandbox %s is still running; stop all sandboxes with the old daemon before migration", metadata.Summary.ID)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func ensureSafeTargetPath(root, destination string) error {
	rel, err := filepath.Rel(root, destination)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target path escapes migration root: %s", destination)
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect target path %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse symlink in target data root: %s", rel)
		}
	}
	return nil
}

func copyFile(source, target string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

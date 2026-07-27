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

func copyAuthoritativeFiles(source, target, runtimeRoot string, schedulerIDs map[string]string) (int, int64, error) {
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
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse symlink in source data root: %s", rel)
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
		if err := copyMigratedFile(path, destination, mappedRel, source, runtimeRoot, schedulerIDs, info.Mode().Perm()); err != nil {
			return err
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
}

func copyMigratedFile(sourcePath, destination, mappedRel, sourceRoot, runtimeRoot string, schedulerIDs map[string]string, mode fs.FileMode) error {
	data, handled, err := rewriteMigratedJSON(sourcePath, mappedRel, sourceRoot, runtimeRoot, schedulerIDs)
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

func rewriteMigratedJSON(sourcePath, mappedRel, sourceRoot, runtimeRoot string, schedulerIDs map[string]string) ([]byte, bool, error) {
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
		expectedPath := filepath.Join(runtimeRoot, "sandboxes", sandboxID)
		if err := rewriteRequiredPath(document, "sandbox_path", sourceRoot, runtimeRoot, expectedPath, schedulerIDs); err != nil {
			return nil, true, fmt.Errorf("rewrite lifecycle sandbox path: %w", err)
		}
		if resources, ok := document["owned_resources"].([]any); ok {
			for _, item := range resources {
				resource, ok := item.(map[string]any)
				if !ok || resource["kind"] != "sandbox-directory" {
					continue
				}
				if err := rewriteRequiredPath(resource, "path", sourceRoot, runtimeRoot, expectedPath, schedulerIDs); err != nil {
					return nil, true, fmt.Errorf("rewrite lifecycle owned resource: %w", err)
				}
			}
		}
	}
	rewritten, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, true, fmt.Errorf("encode migratable JSON %s: %w", mappedRel, err)
	}
	return append(rewritten, '\n'), true, nil
}

func rewriteRequiredPath(container map[string]any, field, sourceRoot, runtimeRoot, expected string, schedulerIDs map[string]string) error {
	value, ok := container[field].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	migrated, inside, err := migratedStoredPath(value, sourceRoot, runtimeRoot, schedulerIDs)
	if err != nil {
		return err
	}
	if !inside {
		return fmt.Errorf("%s %q is outside the legacy data root", field, value)
	}
	if filepath.Clean(migrated) != filepath.Clean(expected) {
		return fmt.Errorf("%s %q does not identify %s", field, value, expected)
	}
	container[field] = migrated
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

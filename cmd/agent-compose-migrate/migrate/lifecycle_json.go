package migrate

import (
	"fmt"
	"path/filepath"
	"strings"

	"agent-compose/pkg/identity"
)

func rewriteLifecyclePaths(document map[string]any, recordID, sourceRoot, runtimeRoot string, schedulerIDs map[string]string) error {
	sandboxID, ok := document["sandbox_id"].(string)
	sandboxID = strings.TrimSpace(sandboxID)
	if !ok || sandboxID == "" {
		return fmt.Errorf("rewrite lifecycle sandbox path: sandbox_id is required")
	}
	if sandboxID != recordID {
		return fmt.Errorf("rewrite lifecycle sandbox path: sandbox_id %q does not match record %q", sandboxID, recordID)
	}

	sandboxPath, err := requiredMigratedLifecyclePath(document, "sandbox_path", sourceRoot, runtimeRoot, schedulerIDs)
	if err != nil {
		return fmt.Errorf("rewrite lifecycle sandbox path: %w", err)
	}
	if err := validateMigratedSandboxPath(sandboxPath, sandboxID, runtimeRoot); err != nil {
		return fmt.Errorf("rewrite lifecycle sandbox path: %w", err)
	}
	document["sandbox_path"] = sandboxPath

	resources, _ := document["owned_resources"].([]any)
	for _, item := range resources {
		resource, ok := item.(map[string]any)
		if !ok || resource["kind"] != "sandbox-directory" {
			continue
		}
		resourcePath, err := requiredMigratedLifecyclePath(resource, "path", sourceRoot, runtimeRoot, schedulerIDs)
		if err != nil {
			return fmt.Errorf("rewrite lifecycle owned resource: %w", err)
		}
		if filepath.Clean(resourcePath) != filepath.Clean(sandboxPath) {
			return fmt.Errorf("rewrite lifecycle owned resource: path %q does not identify %s", resource["path"], sandboxPath)
		}
		resource["path"] = resourcePath
	}
	return nil
}

func requiredMigratedLifecyclePath(container map[string]any, field, sourceRoot, runtimeRoot string, schedulerIDs map[string]string) (string, error) {
	value, ok := container[field].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	migrated, inside, err := migratedStoredPath(value, sourceRoot, runtimeRoot, schedulerIDs)
	if err != nil {
		return "", err
	}
	if !inside {
		return "", fmt.Errorf("%s %q is outside the legacy data root", field, value)
	}
	return migrated, nil
}

func validateMigratedSandboxPath(path, sandboxID, runtimeRoot string) error {
	sandboxRoot := filepath.Join(runtimeRoot, "sandboxes")
	relative, err := filepath.Rel(sandboxRoot, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("sandbox_path %q is outside sandbox root %s", path, sandboxRoot)
	}
	directoryName := filepath.Base(relative)
	if directoryName != sandboxID && directoryName != normalizedSandboxDirectoryName(sandboxID) {
		return fmt.Errorf("sandbox_path %q does not identify sandbox %s", path, sandboxID)
	}
	return nil
}

func normalizedSandboxDirectoryName(sandboxID string) string {
	if hash, err := identity.Hash(sandboxID); err == nil {
		return hash
	}
	return sandboxID
}

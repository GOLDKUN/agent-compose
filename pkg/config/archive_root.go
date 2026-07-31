package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func validateSandboxArchiveRoot(sandboxRoot, archiveRoot string) error {
	resolvedSandboxRoot, err := resolveConfigPathFromExistingAncestor(sandboxRoot)
	if err != nil {
		return fmt.Errorf("resolve SANDBOX_ROOT: %w", err)
	}
	resolvedArchiveRoot, err := resolveConfigPathFromExistingAncestor(archiveRoot)
	if err != nil {
		return fmt.Errorf("resolve SANDBOX_ARCHIVE_ROOT: %w", err)
	}
	relative, err := filepath.Rel(resolvedSandboxRoot, resolvedArchiveRoot)
	if err != nil {
		return fmt.Errorf("compare SANDBOX_ARCHIVE_ROOT with SANDBOX_ROOT: %w", err)
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return fmt.Errorf("SANDBOX_ARCHIVE_ROOT %q must be outside SANDBOX_ROOT %q", archiveRoot, sandboxRoot)
	}
	return nil
}

func resolveConfigPathFromExistingAncestor(path string) (string, error) {
	path = filepath.Clean(path)
	current := path
	var missing []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", resolveErr
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

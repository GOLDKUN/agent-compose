package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func validateMigrationDataRoots(source, target string) error {
	resolvedSource, err := resolvePathWithExistingAncestors(source)
	if err != nil {
		return fmt.Errorf("resolve source root: %w", err)
	}
	resolvedTarget, err := resolvePathWithExistingAncestors(target)
	if err != nil {
		return fmt.Errorf("resolve target root: %w", err)
	}
	if resolvedSource == resolvedTarget {
		return nil
	}
	if pathContains(resolvedSource, resolvedTarget) {
		return fmt.Errorf("target must not be nested inside source")
	}
	if pathContains(resolvedTarget, resolvedSource) {
		return fmt.Errorf("source must not be nested inside target")
	}
	return nil
}

func sameDataRoot(source, target string) (bool, error) {
	resolvedSource, err := resolvePathWithExistingAncestors(source)
	if err != nil {
		return false, fmt.Errorf("resolve source root: %w", err)
	}
	resolvedTarget, err := resolvePathWithExistingAncestors(target)
	if err != nil {
		return false, fmt.Errorf("resolve target root: %w", err)
	}
	return resolvedSource == resolvedTarget, nil
}

func resolvePathWithExistingAncestors(path string) (string, error) {
	path = filepath.Clean(path)
	var missing []string
	current := path
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			parts := append([]string{resolved}, reverseStrings(missing)...)
			return filepath.Clean(filepath.Join(parts...)), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
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

func reverseStrings(values []string) []string {
	result := make([]string, len(values))
	for index := range values {
		result[len(values)-1-index] = values[index]
	}
	return result
}

func pathContains(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

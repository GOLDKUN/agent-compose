package migrate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const inPlaceSymlinkBackupRoot = "symlinks"

func validateMigratableSourceSymlink(rel string) error {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	allowed := false
	if len(parts) > 0 {
		switch parts[0] {
		case "sessions", "sandboxes":
			isMetadata := isSandboxMetadataPath(rel)
			isMountManifest := isSandboxMountManifestPath(rel)
			allowed = len(parts) >= 3 && parts[1] != ".lifecycle" && !isMetadata && !isMountManifest
		case "workspaces":
			allowed = len(parts) >= 4 && parts[2] == "content"
		case "loaders", "schedulers":
			allowed = len(parts) >= 5 && parts[2] == "runs"
		case "volumes":
			allowed = len(parts) >= 3
		}
	}
	if !allowed {
		return fmt.Errorf("refuse symlink in migration-controlled source path: %s", rel)
	}
	return nil
}

func migratedSymlinkTarget(sourcePath, mappedRel, target, sourceRoot, runtimeRoot string, schedulerIDs map[string]string) (string, bool, error) {
	if filepath.IsAbs(target) {
		return migratedStoredPath(target, sourceRoot, runtimeRoot, schedulerIDs)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(sourcePath), target))
	rel, inside, err := relativePathInsideRoot(sourceRoot, resolved)
	if err != nil || !inside {
		return target, false, err
	}
	mappedTargetRel := migratedDataRootPath(rel, schedulerIDs)
	mapped, err := filepath.Rel(filepath.Dir(mappedRel), mappedTargetRel)
	if err != nil {
		return "", false, err
	}
	return mapped, true, nil
}

func relativePathInsideRoot(root, path string) (string, bool, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel, false, nil
	}
	return rel, true, nil
}

func copyMigratedSymlink(source, destination, mappedRel, sourceRoot, targetRoot, runtimeRoot string, schedulerIDs map[string]string) error {
	if err := ensureSafeTargetParent(targetRoot, destination); err != nil {
		return err
	}
	target, err := os.Readlink(source)
	if err != nil {
		return fmt.Errorf("read source symlink %s: %w", source, err)
	}
	migratedTarget, _, err := migratedSymlinkTarget(source, mappedRel, target, sourceRoot, runtimeRoot, schedulerIDs)
	if err != nil {
		return fmt.Errorf("rewrite source symlink %s: %w", source, err)
	}
	return createOrVerifyMigratedSymlink(destination, migratedTarget)
}

func createOrVerifyMigratedSymlink(destination, target string) error {
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Symlink(target, destination); err != nil {
			return fmt.Errorf("create migrated symlink %s: %w", destination, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect migrated symlink %s: %w", destination, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("target path conflicts with source symlink: %s", destination)
	}
	existingTarget, err := os.Readlink(destination)
	if err != nil {
		return fmt.Errorf("read migrated symlink %s: %w", destination, err)
	}
	if existingTarget != target {
		return fmt.Errorf("target symlink %s points to %q instead of %q", destination, existingTarget, target)
	}
	return nil
}

func ensureSafeTargetParent(root, destination string) error {
	parent := filepath.Dir(destination)
	if filepath.Clean(parent) == filepath.Clean(root) {
		return nil
	}
	return ensureSafeTargetPath(root, parent)
}

func rewriteInPlaceSymlinks(root, runtimeRoot string, schedulerIDs map[string]string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		if rel == inPlaceBackupName && entry.IsDir() {
			return filepath.SkipDir
		}
		if entry.IsDir() && skipInPlacePayloadSubtree(rel) {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		if err := validateMigratableSourceSymlink(rel); err != nil {
			return err
		}
		target, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("read in-place symlink %s: %w", rel, err)
		}
		mappedRel := migratedDataRootPath(rel, schedulerIDs)
		migratedTarget, _, err := migratedSymlinkTarget(path, mappedRel, target, root, runtimeRoot, schedulerIDs)
		if err != nil {
			return fmt.Errorf("rewrite in-place symlink %s: %w", rel, err)
		}
		if migratedTarget == target {
			return nil
		}
		backupPath := filepath.Join(root, inPlaceBackupName, inPlaceSymlinkBackupRoot, rel)
		if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
			return fmt.Errorf("create in-place symlink backup directory: %w", err)
		}
		if err := createOrVerifyMigratedSymlink(backupPath, target); err != nil {
			return fmt.Errorf("back up in-place symlink %s: %w", rel, err)
		}
		temporary := path + ".agent-compose-migrate.tmp"
		if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale in-place symlink temporary %s: %w", rel, err)
		}
		if err := os.Symlink(migratedTarget, temporary); err != nil {
			return fmt.Errorf("stage in-place symlink %s: %w", rel, err)
		}
		if err := os.Rename(temporary, path); err != nil {
			return fmt.Errorf("activate in-place symlink %s: %w", rel, err)
		}
		return nil
	})
}

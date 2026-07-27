package migrate

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const inPlaceProgressInterval = 100_000

func inspectInPlaceAuthoritativeFiles(ctx context.Context, root, runtimeRoot string, schedulerIDs map[string]string, agentIDs map[string]standaloneAgentIdentity, progress io.Writer) (int, error) {
	if err := validateInPlaceRenamePlan(root, schedulerIDs); err != nil {
		return 0, err
	}
	files := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		if rel == inPlaceBackupName && entry.IsDir() {
			return filepath.SkipDir
		}
		if rel == databaseName || rel == databaseName+"-wal" || rel == databaseName+"-shm" || rel == journalName {
			return nil
		}
		if entry.IsDir() {
			if skipInPlacePayloadSubtree(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if err := inspectInPlaceSymlink(path, rel, root, runtimeRoot, schedulerIDs); err != nil {
				return err
			}
			files++
			if files%inPlaceProgressInterval == 0 {
				writeMigrationProgress(progress, "files", fmt.Sprintf("checked %d files", files))
			}
			return nil
		}
		mappedRel := migratedDataRootPath(rel, schedulerIDs)
		if !isMigratableJSONPath(mappedRel) {
			return nil
		}
		if entry.Type()&fs.ModeType != 0 {
			return fmt.Errorf("refuse non-regular source file: %s", rel)
		}
		files++
		if files%inPlaceProgressInterval == 0 {
			writeMigrationProgress(progress, "files", fmt.Sprintf("checked %d files", files))
		}
		_, _, err = rewriteMigratedJSON(path, mappedRel, root, runtimeRoot, schedulerIDs, agentIDs)
		return err
	})
	if err != nil {
		return files, err
	}
	writeMigrationProgress(progress, "files", fmt.Sprintf("checked %d files", files))
	return files, nil
}

func skipInPlacePayloadSubtree(rel string) bool {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	if len(parts) >= 4 && parts[0] == "workspaces" && parts[2] == "content" {
		return true
	}
	if len(parts) < 3 || !isSandboxRootName(parts[0]) || parts[1] == ".lifecycle" {
		return false
	}
	sandboxIndex := 1
	if len(parts) >= 4 && validSandboxDatePartition(parts[1:4]) {
		if len(parts) < 6 {
			return false
		}
		sandboxIndex = 4
	} else if isSandboxDatePartitionPrefix(parts[1:]) {
		return false
	}
	if len(parts) <= sandboxIndex+1 {
		return false
	}
	payload := parts[sandboxIndex+1]
	if payload != "workspace" && payload != "vm" {
		return true
	}
	return len(parts) >= sandboxIndex+3
}

func isSandboxDatePartitionPrefix(parts []string) bool {
	if len(parts) == 0 || len(parts) > 2 {
		return false
	}
	probe := append([]string(nil), parts...)
	for len(probe) < 3 {
		probe = append(probe, "01")
	}
	return validSandboxDatePartition(probe)
}

func inspectInPlaceSymlink(path, rel, root, runtimeRoot string, schedulerIDs map[string]string) error {
	if err := validateMigratableSourceSymlink(rel); err != nil {
		return err
	}
	target, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("read source symlink %s: %w", rel, err)
	}
	mappedRel := migratedDataRootPath(rel, schedulerIDs)
	if _, _, err := migratedSymlinkTarget(path, mappedRel, target, root, runtimeRoot, schedulerIDs); err != nil {
		return fmt.Errorf("rewrite source symlink %s: %w", rel, err)
	}
	return nil
}

func isMigratableJSONPath(path string) bool {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	return isSandboxMetadataPath(path) || isSandboxMountManifestPath(path) ||
		(len(parts) == 3 && parts[0] == "sandboxes" && parts[1] == ".lifecycle" && strings.HasSuffix(parts[2], ".json"))
}

func validateInPlaceRenamePlan(root string, schedulerIDs map[string]string) error {
	for _, roots := range [][2]string{{"loaders", "schedulers"}, {"sessions", "sandboxes"}} {
		if err := validateInPlaceRename(filepath.Join(root, roots[0]), filepath.Join(root, roots[1])); err != nil {
			return fmt.Errorf("rename %s directory: %w", roots[0], err)
		}
	}
	legacySchedulers := filepath.Join(root, "loaders")
	legacyIDs := make([]string, 0, len(schedulerIDs))
	for legacyID := range schedulerIDs {
		legacyIDs = append(legacyIDs, legacyID)
	}
	sort.Strings(legacyIDs)
	for _, legacyID := range legacyIDs {
		nativeID := strings.TrimSpace(schedulerIDs[legacyID])
		if err := validateInPlaceIdentity("legacy scheduler", legacyID); err != nil {
			return err
		}
		if err := validateInPlaceIdentity("native scheduler", nativeID); err != nil {
			return err
		}
		if legacyID == nativeID {
			continue
		}
		if err := validateInPlaceRename(filepath.Join(legacySchedulers, legacyID), filepath.Join(legacySchedulers, nativeID)); err != nil {
			return fmt.Errorf("rename scheduler directory %s to %s: %w", legacyID, nativeID, err)
		}
	}
	return nil
}

func validateInPlaceRename(source, target string) error {
	_, sourceErr := os.Lstat(source)
	_, targetErr := os.Lstat(target)
	if sourceErr == nil && targetErr == nil {
		return fmt.Errorf("target already exists")
	}
	if sourceErr != nil && !os.IsNotExist(sourceErr) {
		return sourceErr
	}
	if targetErr != nil && !os.IsNotExist(targetErr) {
		return targetErr
	}
	return nil
}

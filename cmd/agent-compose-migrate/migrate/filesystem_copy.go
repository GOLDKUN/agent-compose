package migrate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func copyAuthoritativeFiles(source, target string, schedulerIDs map[string]string) (int, int64, error) {
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
		if err := copyFile(path, destination, info.Mode().Perm()); err != nil {
			return err
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
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

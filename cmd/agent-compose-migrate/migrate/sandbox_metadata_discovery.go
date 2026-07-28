package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func visitSandboxMetadata(root string, visit func(string) error) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read sandbox root %s: %w", root, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".lifecycle" {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if found, err := visitSandboxMetadataAt(path, visit); err != nil {
			return err
		} else if found {
			continue
		}
		if !validSandboxDatePart(entry.Name(), 4) {
			continue
		}
		if err := visitPartitionedSandboxMetadata(path, entry.Name(), visit); err != nil {
			return err
		}
	}
	return nil
}

func visitPartitionedSandboxMetadata(yearPath, year string, visit func(string) error) error {
	months, err := os.ReadDir(yearPath)
	if err != nil {
		return fmt.Errorf("read sandbox year directory %s: %w", yearPath, err)
	}
	for _, month := range months {
		if !month.IsDir() || !validSandboxDatePart(month.Name(), 2) {
			continue
		}
		monthPath := filepath.Join(yearPath, month.Name())
		days, err := os.ReadDir(monthPath)
		if err != nil {
			return fmt.Errorf("read sandbox month directory %s: %w", monthPath, err)
		}
		for _, day := range days {
			if !day.IsDir() || !validSandboxDatePartition([]string{year, month.Name(), day.Name()}) {
				continue
			}
			dayPath := filepath.Join(monthPath, day.Name())
			sandboxes, err := os.ReadDir(dayPath)
			if err != nil {
				return fmt.Errorf("read sandbox day directory %s: %w", dayPath, err)
			}
			for _, sandbox := range sandboxes {
				if !sandbox.IsDir() {
					continue
				}
				if _, err := visitSandboxMetadataAt(filepath.Join(dayPath, sandbox.Name()), visit); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func visitSandboxMetadataAt(sandboxPath string, visit func(string) error) (bool, error) {
	metadataPath := filepath.Join(sandboxPath, "metadata.json")
	if _, err := os.Lstat(metadataPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect sandbox metadata %s: %w", metadataPath, err)
	}
	return true, visit(metadataPath)
}

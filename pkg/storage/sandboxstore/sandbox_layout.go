package sandboxstore

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type sandboxLocation struct {
	id   string
	path string
}

// sandboxLayout resolves sandbox IDs across the legacy flat layout and the
// date-partitioned layout used for newly created sandboxes. The filesystem
// remains authoritative; the in-memory paths only avoid repeated directory
// scans on normal ID-addressed operations.
type sandboxLayout struct {
	root    string
	readDir func(string) ([]os.DirEntry, error)

	mu    sync.RWMutex
	paths map[string]string
}

func newSandboxLayout(root string) *sandboxLayout {
	return &sandboxLayout{
		root:    root,
		readDir: os.ReadDir,
		paths:   make(map[string]string),
	}
}

func (l *sandboxLayout) path(id string) string {
	key := sandboxDirName(id)
	l.mu.RLock()
	path, ok := l.paths[key]
	l.mu.RUnlock()
	if ok {
		return path
	}
	return filepath.Join(l.root, key)
}

func (l *sandboxLayout) allocate(id string, createdAt time.Time) (string, error) {
	key := sandboxDirName(id)
	path := filepath.Join(
		l.root,
		fmt.Sprintf("%04d", createdAt.Year()),
		fmt.Sprintf("%02d", int(createdAt.Month())),
		fmt.Sprintf("%02d", createdAt.Day()),
		key,
	)
	if err := l.register(key, path); err != nil {
		return "", err
	}
	return path, nil
}

func (l *sandboxLayout) discover() ([]sandboxLocation, error) {
	entries, err := l.readDirectory(l.root)
	if err != nil {
		return nil, fmt.Errorf("read sandbox root: %w", err)
	}

	locations := make([]sandboxLocation, 0, len(entries))
	paths := make(map[string]string)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ".lifecycle" {
			continue
		}
		path := filepath.Join(l.root, entry.Name())
		if sandboxMetadataExists(path) {
			if err := addSandboxLocation(paths, &locations, entry.Name(), path); err != nil {
				return nil, err
			}
			continue
		}
		if !validDatePart(entry.Name(), 4, 1, 9999) {
			continue
		}
		if err := l.discoverSandboxYear(path, entry.Name(), paths, &locations); err != nil {
			return nil, err
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	for id, path := range paths {
		if existing, ok := l.paths[id]; ok && existing != path {
			return nil, fmt.Errorf("sandbox %s exists in multiple directories: %s and %s", id, existing, path)
		}
		l.paths[id] = path
	}
	return locations, nil
}

func (l *sandboxLayout) discoverSandboxYear(yearPath, year string, paths map[string]string, locations *[]sandboxLocation) error {
	months, err := l.readDirectory(yearPath)
	if err != nil {
		warnSkippedSandboxDateDirectory("year", yearPath, err)
		return nil
	}
	for _, month := range months {
		if !month.IsDir() || !validDatePart(month.Name(), 2, 1, 12) {
			continue
		}
		monthPath := filepath.Join(yearPath, month.Name())
		days, err := l.readDirectory(monthPath)
		if err != nil {
			warnSkippedSandboxDateDirectory("month", monthPath, err)
			continue
		}
		for _, day := range days {
			if !day.IsDir() || !validCalendarDay(year, month.Name(), day.Name()) {
				continue
			}
			dayPath := filepath.Join(monthPath, day.Name())
			sandboxes, err := l.readDirectory(dayPath)
			if err != nil {
				warnSkippedSandboxDateDirectory("day", dayPath, err)
				continue
			}
			for _, sandbox := range sandboxes {
				if !sandbox.IsDir() {
					continue
				}
				path := filepath.Join(dayPath, sandbox.Name())
				if !sandboxMetadataExists(path) {
					continue
				}
				if err := addSandboxLocation(paths, locations, sandbox.Name(), path); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (l *sandboxLayout) readDirectory(path string) ([]os.DirEntry, error) {
	if l.readDir != nil {
		return l.readDir(path)
	}
	return os.ReadDir(path)
}

func warnSkippedSandboxDateDirectory(level, path string, err error) {
	slog.Warn("skipping unreadable sandbox date directory", "level", level, "path", path, "error", err)
}

func sandboxMetadataExists(path string) bool {
	info, err := os.Stat(filepath.Join(path, "metadata.json"))
	return err == nil && info.Mode().IsRegular()
}

func addSandboxLocation(paths map[string]string, locations *[]sandboxLocation, id, path string) error {
	key := sandboxDirName(id)
	if existing, ok := paths[key]; ok && existing != path {
		return fmt.Errorf("sandbox %s exists in multiple directories: %s and %s", id, existing, path)
	}
	paths[key] = path
	*locations = append(*locations, sandboxLocation{id: id, path: path})
	return nil
}

func validDatePart(value string, width, minimum, maximum int) bool {
	if len(value) != width {
		return false
	}
	parsed, err := strconv.Atoi(value)
	return err == nil && parsed >= minimum && parsed <= maximum
}

func validCalendarDay(year, month, day string) bool {
	if !validDatePart(day, 2, 1, 31) {
		return false
	}
	yearValue, _ := strconv.Atoi(year)
	monthValue, _ := strconv.Atoi(month)
	dayValue, _ := strconv.Atoi(day)
	date := time.Date(yearValue, time.Month(monthValue), dayValue, 0, 0, 0, 0, time.UTC)
	return date.Year() == yearValue && int(date.Month()) == monthValue && date.Day() == dayValue
}

func (l *sandboxLayout) register(id, path string) error {
	key := sandboxDirName(id)
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, ok := l.paths[key]; ok && existing != path {
		return fmt.Errorf("sandbox %s exists in multiple directories: %s and %s", id, existing, path)
	}
	l.paths[key] = path
	return nil
}

func (l *sandboxLayout) remove(id, path string) {
	key := sandboxDirName(id)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.paths[key] == path {
		delete(l.paths, key)
	}
}

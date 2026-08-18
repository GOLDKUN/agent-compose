package sandboxstore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

func (s *Store) AddEvent(_ context.Context, sessionID string, event SandboxEvent) error {
	unlock := s.lockSandbox(sessionID)
	defer unlock()

	jsonlExisted, err := s.eventsJSONLExists(sessionID)
	if err != nil {
		return err
	}
	legacyCount := 0
	if !jsonlExisted {
		events, err := s.loadLegacyEvents(sessionID)
		if err != nil {
			return err
		}
		legacyCount = len(events)
	}

	if err := s.appendEvent(sessionID, event); err != nil {
		return err
	}

	session, err := s.loadSandbox(sessionID)
	if err != nil {
		slog.Warn("load sandbox summary after committed event append failed", "sandbox_id", sessionID, "error", err)
		return nil
	}
	nextCount := session.Summary.EventCount + 1
	if !jsonlExisted && legacyCount >= session.Summary.EventCount {
		nextCount = legacyCount + 1
	}
	session.Summary.EventCount = nextCount
	if err := s.persistEventSandboxSummary(session); err != nil {
		slog.Warn("sandbox summary update after committed event append failed", "sandbox_id", sessionID, "error", err)
	}
	return nil
}

func (s *Store) persistEventSandboxSummary(session *Sandbox) error {
	s.hydrateSandboxGuestImage(session)
	session.Summary.UpdatedAt = s.currentTime().UTC()
	if err := s.saveSandboxPreservingCounts(session); err != nil {
		return fmt.Errorf("save sandbox summary after event append: %w", err)
	}
	s.recordIndex(session)
	return nil
}

func (s *Store) ListEvents(_ context.Context, id string) ([]SandboxEvent, error) {
	unlock := s.lockSandbox(id)
	defer unlock()
	return s.loadEvents(id)
}

func (s *Store) loadEvents(id string) ([]SandboxEvent, error) {
	events, err := s.loadLegacyEvents(id)
	if err != nil {
		return nil, err
	}
	jsonlEvents, err := s.loadJSONLEvents(id)
	if err != nil {
		return nil, err
	}
	return append(events, jsonlEvents...), nil
}

func (s *Store) eventsJSONPath(id string) string {
	return filepath.Join(s.sandboxDir(id), "state", "events.json")
}

func (s *Store) eventsJSONLPath(id string) string {
	return filepath.Join(s.sandboxDir(id), "state", "events.jsonl")
}

func (s *Store) eventsJSONLExists(id string) (bool, error) {
	_, err := os.Stat(s.eventsJSONLPath(id))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("stat events jsonl: %w", err)
}

func (s *Store) loadLegacyEvents(id string) ([]SandboxEvent, error) {
	path := s.eventsJSONPath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read events: %w", err)
	}
	var events []SandboxEvent
	if len(data) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}
	return events, nil
}

func (s *Store) loadJSONLEvents(id string) ([]SandboxEvent, error) {
	path := s.eventsJSONLPath(id)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read events jsonl: %w", err)
	}
	defer func() { _ = file.Close() }()

	reader := bufio.NewReader(file)
	var events []SandboxEvent
	lineNumber := 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineNumber++
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				var event SandboxEvent
				if err := json.Unmarshal(line, &event); err != nil {
					return nil, fmt.Errorf("decode events %s line %d: %w", path, lineNumber, err)
				}
				events = append(events, event)
			}
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		return nil, fmt.Errorf("read events %s line %d: %w", path, lineNumber+1, readErr)
	}
	return events, nil
}

func (s *Store) appendEvent(id string, event SandboxEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	data = append(data, '\n')

	file, err := os.OpenFile(s.eventsJSONLPath(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open events jsonl: %w", err)
	}
	if n, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("append events jsonl: %w", err)
	} else if n != len(data) {
		_ = file.Close()
		return fmt.Errorf("append events jsonl: %w", io.ErrShortWrite)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close events jsonl: %w", err)
	}
	return nil
}

func (s *Store) saveEvents(id string, events []SandboxEvent) error {
	file, err := os.OpenFile(s.eventsJSONLPath(id), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("write events jsonl: %w", err)
	}
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			_ = file.Close()
			return fmt.Errorf("encode event: %w", err)
		}
		data = append(data, '\n')
		if n, err := file.Write(data); err != nil {
			_ = file.Close()
			return fmt.Errorf("write events jsonl: %w", err)
		} else if n != len(data) {
			_ = file.Close()
			return fmt.Errorf("write events jsonl: %w", io.ErrShortWrite)
		}
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close events jsonl: %w", err)
	}
	if err := os.Remove(s.eventsJSONPath(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove legacy events: %w", err)
	}
	return nil
}

func (s *Store) SaveEvents(id string, events []SandboxEvent) error {
	unlock := s.lockSandbox(id)
	defer unlock()
	if err := s.saveEvents(id, events); err != nil {
		return err
	}
	return s.saveEventCount(id, len(events))
}

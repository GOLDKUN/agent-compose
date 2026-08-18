package sandboxstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *Store) AddCell(_ context.Context, session *Sandbox, cell NotebookCell) error {
	cells, err := s.loadCells(session.Summary.ID)
	if err != nil {
		return err
	}
	updated := false
	if strings.TrimSpace(cell.ID) != "" {
		for index := range cells {
			if cells[index].ID != cell.ID {
				continue
			}
			cells[index] = cell
			updated = true
			break
		}
	}
	if !updated {
		cells = append(cells, cell)
	}
	if err := s.saveCells(session.Summary.ID, cells); err != nil {
		return err
	}
	timelineCells, err := s.loadCells(session.Summary.ID)
	if err != nil {
		return err
	}
	session.Summary.CellCount = len(timelineCells)
	return s.UpdateSandbox(context.Background(), session)
}

func (s *Store) ListCells(_ context.Context, id string) ([]NotebookCell, error) {
	return s.loadCells(id)
}

func (s *Store) AddAgentRun(_ context.Context, sessionID string, run AgentRun) error {
	cell := NotebookCell{
		ID:            run.ID,
		Type:          CellTypeAgent,
		Source:        run.Message,
		Output:        run.Output,
		ExitCode:      run.ExitCode,
		Success:       run.Success,
		Running:       run.Running,
		CreatedAt:     run.CreatedAt,
		Agent:         run.Agent,
		AgentThreadID: run.AgentThreadID,
		StopReason:    run.StopReason,
	}
	session, err := s.loadSandbox(sessionID)
	if err != nil {
		return err
	}
	if err := s.AddCell(context.Background(), session, cell); err != nil {
		return err
	}
	session, err = s.loadSandbox(sessionID)
	if err == nil {
		timelineCells, loadErr := s.loadCells(sessionID)
		if loadErr == nil {
			session.Summary.CellCount = len(timelineCells)
		}
		_ = s.UpdateSandbox(context.Background(), session)
	}
	return nil
}

func (s *Store) loadCells(id string) ([]NotebookCell, error) {
	path := filepath.Join(s.sandboxDir(id), "state", "cells.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cells: %w", err)
	}
	var cells []NotebookCell
	if len(data) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &cells); err != nil {
		return nil, fmt.Errorf("decode cells: %w", err)
	}
	migrated, changed, err := s.mergeLegacyAgentRuns(id, cells)
	if err != nil {
		return nil, err
	}
	if changed {
		if err := s.saveCells(id, migrated); err != nil {
			return nil, err
		}
	}
	for index := range migrated {
		migrated[index] = hydrateRunningCellArtifacts(filepath.Join(s.sandboxDir(id), "state", "cells", migrated[index].ID), migrated[index])
	}
	return migrated, nil
}

func hydrateRunningCellArtifacts(cellDir string, cell NotebookCell) NotebookCell {
	if !cell.Running || strings.TrimSpace(cell.ID) == "" {
		return cell
	}
	loadArtifact := func(name, current string) string {
		data, err := os.ReadFile(filepath.Join(cellDir, name))
		if err != nil {
			return current
		}
		value := string(data)
		if len(value) <= len(current) {
			return current
		}
		return value
	}
	cell.Stdout = loadArtifact("stdout.txt", cell.Stdout)
	cell.Stderr = loadArtifact("stderr.txt", cell.Stderr)
	cell.Output = loadArtifact("output.txt", firstNonEmpty(cell.Output, cell.Stdout+cell.Stderr))
	return cell
}

func (s *Store) saveCells(id string, cells []NotebookCell) error {
	data, err := json.MarshalIndent(cells, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cells: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.sandboxDir(id), "state", "cells.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write cells: %w", err)
	}
	return nil
}

func (s *Store) SaveCells(id string, cells []NotebookCell) error {
	return s.saveCells(id, cells)
}

func (s *Store) loadAgentRuns(id string) ([]AgentRun, error) {
	path := filepath.Join(s.sandboxDir(id), "state", "agent_runs.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agent runs: %w", err)
	}
	var runs []AgentRun
	if len(data) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(data, &runs); err != nil {
		return nil, fmt.Errorf("decode agent runs: %w", err)
	}
	return runs, nil
}

func (s *Store) mergeLegacyAgentRuns(id string, cells []NotebookCell) ([]NotebookCell, bool, error) {
	runs, err := s.loadAgentRuns(id)
	if err != nil {
		return nil, false, err
	}
	if len(runs) == 0 {
		sort.SliceStable(cells, func(i, j int) bool {
			if cells[i].CreatedAt.Equal(cells[j].CreatedAt) {
				return cells[i].ID < cells[j].ID
			}
			return cells[i].CreatedAt.Before(cells[j].CreatedAt)
		})
		return cells, false, nil
	}
	seen := make(map[string]struct{}, len(cells))
	merged := append([]NotebookCell(nil), cells...)
	for _, cell := range merged {
		seen[cell.ID] = struct{}{}
	}
	changed := false
	for _, run := range runs {
		if _, ok := seen[run.ID]; ok {
			continue
		}
		merged = append(merged, NotebookCell{
			ID:            run.ID,
			Type:          CellTypeAgent,
			Source:        run.Message,
			Output:        run.Output,
			ExitCode:      run.ExitCode,
			Success:       run.Success,
			CreatedAt:     run.CreatedAt,
			Agent:         run.Agent,
			AgentThreadID: run.AgentThreadID,
			StopReason:    run.StopReason,
		})
		changed = true
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].CreatedAt.Equal(merged[j].CreatedAt) {
			return merged[i].ID < merged[j].ID
		}
		return merged[i].CreatedAt.Before(merged[j].CreatedAt)
	})
	return merged, changed, nil
}

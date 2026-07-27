package api

import (
	"testing"
	"time"

	domain "agent-compose/pkg/model"
)

func TestPaginateSandboxHistoryUsesOneStableTimeline(t *testing.T) {
	base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	cells := []domain.NotebookCell{
		{ID: "cell-newest", CreatedAt: base.Add(3 * time.Minute)},
		{ID: "cell-oldest", CreatedAt: base},
	}
	events := []domain.SandboxEvent{
		{ID: "event-middle", CreatedAt: base.Add(2 * time.Minute)},
		{ID: "event-older", CreatedAt: base.Add(time.Minute)},
	}

	response, err := paginateSandboxHistory(cells, events, 1, 2)
	if err != nil {
		t.Fatalf("paginateSandboxHistory returned error: %v", err)
	}
	if response.GetTotal() != 4 {
		t.Fatalf("total = %d, want 4", response.GetTotal())
	}
	if len(response.GetCells()) != 0 {
		t.Fatalf("cells = %#v, want no cells on the middle page", response.GetCells())
	}
	if got := response.GetEvents(); len(got) != 2 || got[0].GetId() != "event-middle" || got[1].GetId() != "event-older" {
		t.Fatalf("events = %#v, want middle timeline entries", got)
	}
}

func TestPaginateSandboxHistoryBreaksTimestampTiesDeterministically(t *testing.T) {
	createdAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	cells := []domain.NotebookCell{{ID: "cell-b", CreatedAt: createdAt}, {ID: "cell-a", CreatedAt: createdAt}}
	events := []domain.SandboxEvent{{ID: "event-z", CreatedAt: createdAt}}

	first, err := paginateSandboxHistory(cells, events, 0, 1)
	if err != nil {
		t.Fatalf("first page returned error: %v", err)
	}
	second, err := paginateSandboxHistory(cells, events, 1, 1)
	if err != nil {
		t.Fatalf("second page returned error: %v", err)
	}
	third, err := paginateSandboxHistory(cells, events, 2, 1)
	if err != nil {
		t.Fatalf("third page returned error: %v", err)
	}
	if first.GetCells()[0].GetId() != "cell-b" || second.GetCells()[0].GetId() != "cell-a" || third.GetEvents()[0].GetId() != "event-z" {
		t.Fatalf("tie order = (%v, %v, %v), want cell-b, cell-a, event-z", first, second, third)
	}
}

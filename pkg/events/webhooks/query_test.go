package webhooks

import (
	"context"
	"errors"
	"slices"
	"testing"

	domain "agent-compose/pkg/model"
)

func TestTraceServiceKeepsTraceWhenSandboxSummaryEnrichmentFails(t *testing.T) {
	summaryErr := errors.New("sandbox projection unavailable")
	reader := &sandboxSummaryReaderStub{
		summaries: map[string]domain.SandboxSummary{
			"sandbox-1": {ID: "sandbox-1", Title: "available"},
		},
		err: summaryErr,
	}
	service := newTraceService(eventTraceStoreStub{trace: domain.EventTrace{
		Event: domain.EventSummary{ID: "event-1"},
		SandboxLinks: []domain.EventSandboxTraceItem{
			{SandboxID: "sandbox-1"},
			{SandboxID: " sandbox-1 "},
			{SandboxID: "missing"},
			{},
		},
	}}, reader)

	view, err := service.trace(context.Background(), "event-1")
	if err != nil {
		t.Fatalf("trace returned error: %v", err)
	}
	if !errors.Is(view.SandboxSummaryError, summaryErr) || view.Sandboxes["sandbox-1"].Title != "available" {
		t.Fatalf("trace view = %#v", view)
	}
	if !slices.Equal(reader.ids, []string{"sandbox-1", "missing"}) {
		t.Fatalf("sandbox summary ids = %#v", reader.ids)
	}
}

func TestTraceServicePropagatesSandboxSummaryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := newTraceService(eventTraceStoreStub{trace: domain.EventTrace{
		Event:        domain.EventSummary{ID: "event-1"},
		SandboxLinks: []domain.EventSandboxTraceItem{{SandboxID: "sandbox-1"}},
	}}, &sandboxSummaryReaderStub{err: context.Canceled})

	if _, err := service.trace(ctx, "event-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("trace error = %v, want context canceled", err)
	}
}

type eventTraceStoreStub struct {
	trace domain.EventTrace
	err   error
}

func (s eventTraceStoreStub) GetEventTrace(context.Context, string, int) (domain.EventTrace, error) {
	return s.trace, s.err
}

type sandboxSummaryReaderStub struct {
	ids       []string
	summaries map[string]domain.SandboxSummary
	err       error
}

func (s *sandboxSummaryReaderStub) ListSandboxSummaries(_ context.Context, ids []string) (map[string]domain.SandboxSummary, error) {
	s.ids = append([]string(nil), ids...)
	return s.summaries, s.err
}

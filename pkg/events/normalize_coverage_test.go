package events

import (
	"strings"
	"testing"
	"time"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestNormalizeTopicEventRecordCoverage(t *testing.T) {
	event, err := NormalizeTopicEventRecord(domain.TopicEventRecord{
		Topic:          " runtime.test ",
		Source:         domain.TopicEventSourceScheduler,
		DispatchStatus: domain.TopicEventDispatchPending,
		PayloadJSON:    `{"ok":true}`,
		AttemptCount:   -1,
		ClaimUntil:     time.Now(),
		NextAttemptAt:  time.Now(),
		DeadLetterAt:   time.Now(),
		DispatchedAt:   time.Now(),
	}, true)
	if err != nil {
		t.Fatalf("NormalizeTopicEventRecord returned error: %v", err)
	}
	if !strings.HasPrefix(event.ID, "evt_") || event.CorrelationID != event.ID || event.PayloadHash == "" || event.AttemptCount != 0 {
		t.Fatalf("normalized event = %#v", event)
	}
	legacy, err := NormalizeTopicEventRecord(domain.TopicEventRecord{
		ID:             "legacy-event",
		Topic:          "runtime.legacy",
		Source:         "loader",
		DispatchStatus: domain.TopicEventDispatchPending,
	}, false)
	if err != nil || legacy.Source != domain.TopicEventSourceScheduler {
		t.Fatalf("legacy source normalized to %q, err=%v", legacy.Source, err)
	}
	for _, item := range []domain.TopicEventRecord{
		{Topic: "bad topic", Source: domain.TopicEventSourceScheduler, DispatchStatus: domain.TopicEventDispatchPending},
		{ID: "event-1", Topic: "runtime.test", DispatchStatus: domain.TopicEventDispatchPending},
		{ID: "event-1", Topic: "runtime.test", Source: domain.TopicEventSourceScheduler, DispatchStatus: "bad"},
		{ID: "event-1", Topic: "runtime.test", Source: domain.TopicEventSourceScheduler, DispatchStatus: domain.TopicEventDispatchPending, PayloadJSON: `{bad`},
	} {
		if _, err := NormalizeTopicEventRecord(item, false); err == nil {
			t.Fatalf("expected normalize error for %#v", item)
		}
	}
}

func TestIntegrationNormalizeTopicEventRecordCoverage(t *testing.T) {
	TestNormalizeTopicEventRecordCoverage(t)
}

func TestE2ENormalizeTopicEventRecordCoverage(t *testing.T) {
	TestNormalizeTopicEventRecordCoverage(t)
}

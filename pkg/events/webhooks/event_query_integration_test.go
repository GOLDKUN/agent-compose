package webhooks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chaitin/agent-compose/pkg/events/webhooks"
	domain "github.com/chaitin/agent-compose/pkg/model"
	"github.com/chaitin/agent-compose/pkg/storage/configstore"
	"github.com/chaitin/agent-compose/pkg/storage/sqlite"

	"github.com/labstack/echo/v4"
)

func TestIntegrationEventQueryHTTPWorkflow(t *testing.T) {
	testEventQueryHTTPWorkflow(t)
}

func TestE2EEventQueryHTTPWorkflow(t *testing.T) {
	testEventQueryHTTPWorkflow(t)
}

func testEventQueryHTTPWorkflow(t *testing.T) {
	ctx := context.Background()
	database, err := sqlite.Open(filepath.Join(t.TempDir(), "data.db"), 0)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	store := configstore.FromDB(database.DB())
	root, err := store.CreateEvent(ctx, domain.TopicEventRecord{
		ID: "webhook-root", Topic: "webhook.github.push", Source: domain.TopicEventSourceWebhook,
		IdempotencyKey: "private-idempotency-key",
		PayloadJSON:    `{"secret":"not-for-collection-views"}`, DispatchStatus: domain.TopicEventDispatchPublishedToBus,
	})
	if err != nil {
		t.Fatalf("create root event: %v", err)
	}
	child, err := store.CreateEvent(ctx, domain.TopicEventRecord{
		ID: "scheduler-child", Topic: "runtime.completed", Source: domain.TopicEventSourceScheduler,
		CorrelationID: root.CorrelationID, ParentEventID: root.ID,
		PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPublishedToBus,
	})
	if err != nil {
		t.Fatalf("create child event: %v", err)
	}
	if err := store.UpsertEventDelivery(ctx, domain.EventDelivery{
		EventID: child.ID, SchedulerID: "scheduler-1", TriggerID: "trigger-1",
		Status: domain.EventDeliveryStatusMatched,
	}); err != nil {
		t.Fatalf("upsert child delivery: %v", err)
	}
	if err := store.AddEventSandboxLink(ctx, domain.EventSandboxLink{
		EventID: child.ID, SandboxID: "sandbox-1", Relation: "scheduler.completed",
		SchedulerID: "scheduler-1", TriggerID: "trigger-1",
	}); err != nil {
		t.Fatalf("add child sandbox link: %v", err)
	}
	forwarded, err := store.CreateEvent(ctx, domain.TopicEventRecord{
		ID: "webhook-forwarded", Topic: "webhook.forwarded.topic", Source: domain.TopicEventSourceWebhook,
		CorrelationID: root.CorrelationID, PayloadJSON: `{}`, DispatchStatus: domain.TopicEventDispatchPublishedToBus,
	})
	if err != nil {
		t.Fatalf("create forwarded event: %v", err)
	}
	if err := store.UpsertEventDelivery(ctx, domain.EventDelivery{
		EventID: forwarded.ID, SchedulerID: "scheduler-2", TriggerID: "trigger-2",
		Status: domain.EventDeliveryStatusMatched,
	}); err != nil {
		t.Fatalf("upsert forwarded delivery: %v", err)
	}
	if err := store.AddEventSandboxLink(ctx, domain.EventSandboxLink{
		EventID: forwarded.ID, SandboxID: "sandbox-2", Relation: "scheduler.completed",
		SchedulerID: "scheduler-2", TriggerID: "trigger-2",
	}); err != nil {
		t.Fatalf("add forwarded sandbox link: %v", err)
	}

	app := echo.New()
	webhooks.RegisterRoutes(app, webhooks.RouteOptions{Store: store, QueryStore: store})

	summaryBody := getEventQueryResponse(t, app, "/api/events?source=webhook&view=summary&offset=0&limit=10")
	if strings.Contains(summaryBody, "private-idempotency-key") || strings.Contains(summaryBody, "not-for-collection-views") {
		t.Fatalf("summary response exposed private event fields: %s", summaryBody)
	}
	var summaries webhooks.EventSummaryListResponse
	if err := json.Unmarshal([]byte(summaryBody), &summaries); err != nil {
		t.Fatalf("decode summary response: %v", err)
	}
	if summaries.Total != 2 || len(summaries.Items) != 2 {
		t.Fatalf("summary response = %#v", summaries)
	}

	var topics webhooks.EventTopicListResponse
	if err := json.Unmarshal([]byte(getEventQueryResponse(t, app, "/api/events/topics?source=webhook")), &topics); err != nil {
		t.Fatalf("decode topic response: %v", err)
	}
	if topics.Total != 2 || len(topics.Items) != 2 {
		t.Fatalf("topic response = %#v", topics)
	}

	var trace webhooks.EventTraceResponse
	if err := json.Unmarshal([]byte(getEventQueryResponse(t, app, "/api/events/"+root.ID+"/trace")), &trace); err != nil {
		t.Fatalf("decode trace response: %v", err)
	}
	if trace.Event.EventID != root.ID || len(trace.Runs) != 2 {
		t.Fatalf("trace runs = %#v", trace)
	}
	runEvents := map[string]bool{}
	for _, run := range trace.Runs {
		runEvents[run.Delivery.EventID] = true
	}
	if !runEvents[child.ID] || !runEvents[forwarded.ID] {
		t.Fatalf("trace run events = %#v, want child and correlation sibling", runEvents)
	}
	if len(trace.Sandboxes) != 2 {
		t.Fatalf("trace sandboxes = %#v", trace.Sandboxes)
	}
	sandboxEvents := map[string]string{}
	for _, sandbox := range trace.Sandboxes {
		sandboxEvents[sandbox.EventID] = sandbox.SandboxID
	}
	if sandboxEvents[child.ID] != "sandbox-1" || sandboxEvents[forwarded.ID] != "sandbox-2" {
		t.Fatalf("trace sandbox events = %#v, want child and correlation sibling", sandboxEvents)
	}
}

func getEventQueryResponse(t *testing.T, app *echo.Echo, target string) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d body=%s", target, recorder.Code, recorder.Body.String())
	}
	return recorder.Body.String()
}

package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	domain "agent-compose/pkg/model"
)

func TestWebhookPayloadConflictReturnsExistingEvent(t *testing.T) {
	existing := webhookConflictEvent()
	store := newWebhookRouteStore()
	store.events[existing.ID] = existing
	store.existingID = existing.ID

	response := postWebhookWithIdempotencyKey(t, store, `{"intent":"changed"}`)
	assertIdempotencyConflictResponse(t, response, existing)
	if len(store.events) != 1 {
		t.Fatalf("stored events = %d, want 1", len(store.events))
	}
}

func TestWebhookConcurrentPayloadConflictReturnsExistingEvent(t *testing.T) {
	existing := webhookConflictEvent()
	base := newWebhookRouteStore()
	base.events[existing.ID] = existing
	store := &concurrentWebhookConflictStore{
		webhookRouteStore: base,
		existing:          existing,
	}

	response := postWebhookWithIdempotencyKey(t, store, `{"intent":"changed"}`)
	assertIdempotencyConflictResponse(t, response, existing)
	if store.lookupCount != 1 {
		t.Fatalf("idempotency lookups = %d, want 1", store.lookupCount)
	}
}

func TestWebhookConcurrentIdenticalPayloadReturnsExistingEvent(t *testing.T) {
	existing := webhookConflictEvent()
	base := newWebhookRouteStore()
	base.events[existing.ID] = existing
	store := &concurrentWebhookConflictStore{
		webhookRouteStore: base,
		existing:          existing,
	}

	response := postWebhookWithIdempotencyKey(t, store, `{"intent":"original"}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want %d", response.Code, response.Body.String(), http.StatusAccepted)
	}
	var accepted AcceptedResponse
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !accepted.Accepted || accepted.EventID != existing.ID || accepted.Topic != existing.Topic {
		t.Fatalf("accepted response = %#v", accepted)
	}
	if store.lookupCount != 1 {
		t.Fatalf("idempotency lookups = %d, want 1", store.lookupCount)
	}
}

func TestWebhookIdempotencyComparesCanonicalRequestBodyOnly(t *testing.T) {
	existing := webhookConflictEvent()
	existing.PayloadJSON = `{
		"body":{"intent":"original"},
		"correlationId":"correlation-original",
		"headers":{"user-agent":"original-agent"},
		"query":{"delivery":"original"}
	}`
	store := newWebhookRouteStore()
	store.events[existing.ID] = existing
	store.existingID = existing.ID

	response := postWebhookWithHeaders(t, store, `{"intent":"original"}`, http.Header{
		"User-Agent":       {"changed-agent"},
		"X-Correlation-ID": {"correlation-changed"},
	})
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want %d", response.Code, response.Body.String(), http.StatusAccepted)
	}
	var accepted AcceptedResponse
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if accepted.EventID != existing.ID || accepted.CorrelationID != existing.CorrelationID {
		t.Fatalf("accepted response = %#v", accepted)
	}
}

func TestWebhookConflictWithoutExistingEventReturnsInternalError(t *testing.T) {
	store := &untypedWebhookConflictStore{webhookRouteStore: newWebhookRouteStore()}
	response := postWebhookWithIdempotencyKey(t, store, `{"intent":"original"}`)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s, want %d", response.Code, response.Body.String(), http.StatusInternalServerError)
	}
}

func webhookConflictEvent() domain.TopicEventRecord {
	return domain.TopicEventRecord{
		ID:             "event-existing",
		Topic:          "webhook.github.push",
		CorrelationID:  "correlation-existing",
		IdempotencyKey: "idem-existing",
		PayloadJSON:    `{"body":{"intent":"original"}}`,
		Sequence:       17,
	}
}

func postWebhookWithIdempotencyKey(t *testing.T, store Store, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postWebhookWithHeaders(t, store, body, nil)
}

func postWebhookWithHeaders(t *testing.T, store Store, body string, headers http.Header) *httptest.ResponseRecorder {
	t.Helper()
	app := echo.New()
	RegisterRoutes(app, RouteOptions{
		Store:            store,
		WebhookBodyLimit: 1 << 20,
		NewEventID:       func() string { return "event-new" },
	})
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/webhook.github.push", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-existing")
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func assertIdempotencyConflictResponse(t *testing.T, rec *httptest.ResponseRecorder, existing domain.TopicEventRecord) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusConflict)
	}
	var response idempotencyConflictResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != idempotencyPayloadMismatchCode || response.Error != idempotencyPayloadMismatchMessage {
		t.Fatalf("conflict response = %#v", response)
	}
	if !response.ExistingEvent.Accepted || response.ExistingEvent.EventID != existing.ID ||
		response.ExistingEvent.Topic != existing.Topic || response.ExistingEvent.Sequence != existing.Sequence ||
		response.ExistingEvent.CorrelationID != existing.CorrelationID {
		t.Fatalf("existing event = %#v, want %#v", response.ExistingEvent, existing)
	}
}

type concurrentWebhookConflictStore struct {
	*webhookRouteStore
	existing    domain.TopicEventRecord
	lookupCount int
}

func (s *concurrentWebhookConflictStore) FindEventByIdempotencyKey(context.Context, string, string) (domain.TopicEventRecord, bool, error) {
	s.lookupCount++
	if s.lookupCount == 1 {
		return domain.TopicEventRecord{}, false, nil
	}
	return domain.TopicEventRecord{}, false, errors.New("unexpected second idempotency lookup")
}

func (s *concurrentWebhookConflictStore) CreateEvent(context.Context, domain.TopicEventRecord) (domain.TopicEventRecord, error) {
	return domain.TopicEventRecord{}, &domain.TopicEventIdempotencyConflictError{Existing: s.existing}
}

type untypedWebhookConflictStore struct {
	*webhookRouteStore
}

func (s *untypedWebhookConflictStore) CreateEvent(context.Context, domain.TopicEventRecord) (domain.TopicEventRecord, error) {
	return domain.TopicEventRecord{}, domain.ErrConflict
}

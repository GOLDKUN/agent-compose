package webhooks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	domain "agent-compose/pkg/model"

	"github.com/labstack/echo/v4"
)

func TestWebhookReturnsAcceptedWhenContextIsCanceledAfterEventCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &cancelAfterCreateWebhookStore{
		webhookRouteStore: newWebhookRouteStore(),
		cancel:            cancel,
	}
	app := echo.New()
	RegisterRoutes(app, RouteOptions{
		Store:            store,
		WebhookBodyLimit: 1 << 20,
		NewEventID:       func() string { return "event-committed" },
	})

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/webhook.github.push", strings.NewReader(`{"intent":"push"}`)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusAccepted)
	}
	if len(store.events) != 1 {
		t.Fatalf("committed events = %d, want 1", len(store.events))
	}
}

type cancelAfterCreateWebhookStore struct {
	*webhookRouteStore
	cancel context.CancelFunc
}

func (s *cancelAfterCreateWebhookStore) CreateEvent(ctx context.Context, event domain.TopicEventRecord) (domain.TopicEventRecord, error) {
	created, err := s.webhookRouteStore.CreateEvent(ctx, event)
	if err == nil {
		s.cancel()
	}
	return created, err
}

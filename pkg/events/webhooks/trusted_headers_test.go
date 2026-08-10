package webhooks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	domain "agent-compose/pkg/model"

	"github.com/labstack/echo/v4"
)

func TestWebhookPayloadIncludesTrustedMPIHeaders(t *testing.T) {
	store := newWebhookRouteStore()
	app := newWebhookTestApp(store)
	ctx := domain.NewContextWithTrustedHeaders(context.Background(), []domain.TrustedHeader{
		{Name: "x-mpi-user-id", Value: "user-1"},
		{Name: "X-MPI-ROLE", Value: "admin"},
		{Name: "x-mpi-role", Value: "auditor"},
		{Name: "x-not-mpi", Value: "ignored"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/webhook.github.push", strings.NewReader(`{"intent":"push"}`)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(store.events) != 1 {
		t.Fatalf("stored events = %d, want 1", len(store.events))
	}
	var payload struct {
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal([]byte(store.events["event-trusted-headers"].PayloadJSON), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Headers["x-mpi-user-id"] != "user-1" || payload.Headers["x-mpi-role"] != "admin,auditor" {
		t.Fatalf("trusted headers = %#v", payload.Headers)
	}
	if _, ok := payload.Headers["x-not-mpi"]; ok {
		t.Fatalf("non-MPI trusted header was persisted: %#v", payload.Headers)
	}
}

func TestWebhookPayloadRejectsUntrustedMPIHeaders(t *testing.T) {
	store := newWebhookRouteStore()
	app := newWebhookTestApp(store)
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/webhook.github.push", strings.NewReader(`{"intent":"push"}`))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MPI-User-ID", "forged-user")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("webhook status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal([]byte(store.events["event-trusted-headers"].PayloadJSON), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if _, ok := payload.Headers["x-mpi-user-id"]; ok {
		t.Fatalf("untrusted MPI header was persisted: %#v", payload.Headers)
	}
}

func newWebhookTestApp(store *webhookRouteStore) *echo.Echo {
	app := echo.New()
	RegisterRoutes(app, RouteOptions{
		Store: store, WebhookBodyLimit: 1 << 20, NewEventID: func() string { return "event-trusted-headers" },
	})
	return app
}

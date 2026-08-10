package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent-compose/pkg/events/webhooks"
	domain "agent-compose/pkg/model"

	"github.com/labstack/echo/v4"
)

func TestDaemonTrustedHeadersReachWebhookPayload(t *testing.T) {
	app := echo.New()
	app.Use(newDaemonTrustedHeadersMiddleware())
	app.POST("/api/webhooks/:topic", func(c echo.Context) error {
		payload := webhooks.BuildPayload(
			c.Request(), "event-1", 1, c.Param("topic"), "correlation-1", "idempotency-1",
			domain.WebhookSource{}, map[string]any{"intent": "push"},
		)
		return c.JSON(http.StatusAccepted, payload)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/webhook.github.push", nil)
	req.Header.Add("X-MPI-Role", "admin")
	req.Header.Add("X-MPI-Role", "auditor")
	req.Header.Set("X-MPI-User-ID", "user-1")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode webhook payload: %v", err)
	}
	if payload.Headers["x-mpi-role"] != "admin,auditor" || payload.Headers["x-mpi-user-id"] != "user-1" {
		t.Fatalf("trusted headers = %#v", payload.Headers)
	}
}

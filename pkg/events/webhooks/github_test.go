package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	domain "agent-compose/pkg/model"
)

func TestGitHubWebhookSignatureAndEventRouting(t *testing.T) {
	for _, event := range []string{"push", "pull_request", "ping"} {
		t.Run(event, func(t *testing.T) {
			store, app := newGitHubWebhookTestServer()
			body := `{"zen":"Keep it logically awesome."}`
			rec := deliverGitHubWebhook(app, body, event, "delivery-"+event, signGitHubBody(body, "github-secret"))
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			eventRecord := store.events["event-1"]
			if want := "webhook.github." + event; eventRecord.Topic != want {
				t.Fatalf("topic = %q, want %q", eventRecord.Topic, want)
			}
			if eventRecord.DeliveryID != "delivery-"+event || eventRecord.IdempotencyKey != "delivery-"+event {
				t.Fatalf("delivery identifiers = %q/%q", eventRecord.DeliveryID, eventRecord.IdempotencyKey)
			}
		})
	}
}

func TestGitHubWebhookRejectsInvalidAuthentication(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		signature string
		event     string
		want      int
	}{
		{name: "missing signature", body: `{}`, event: "push", want: http.StatusUnauthorized},
		{name: "malformed signature", body: `{}`, signature: "sha256=xyz", event: "push", want: http.StatusUnauthorized},
		{name: "wrong signature", body: `{}`, signature: signGitHubBody(`{"different":true}`, "github-secret"), event: "push", want: http.StatusUnauthorized},
		{name: "tampered body", body: `{"tampered":true}`, signature: signGitHubBody(`{"tampered":false}`, "github-secret"), event: "push", want: http.StatusUnauthorized},
		{name: "missing event", body: `{}`, signature: signGitHubBody(`{}`, "github-secret"), want: http.StatusBadRequest},
		{name: "invalid event", body: `{}`, signature: signGitHubBody(`{}`, "github-secret"), event: "push/invalid", want: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, app := newGitHubWebhookTestServer()
			rec := deliverGitHubWebhook(app, test.body, test.event, "delivery-1", test.signature)
			if rec.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, test.want, rec.Body.String())
			}
			if len(store.events) != 0 {
				t.Fatalf("stored events = %#v", store.events)
			}
		})
	}
}

func TestGitHubWebhookRedeliveryIsIdempotent(t *testing.T) {
	store, app := newGitHubWebhookTestServer()
	body := `{"ref":"refs/heads/main"}`
	signature := signGitHubBody(body, "github-secret")
	first := deliverGitHubWebhook(app, body, "push", "same-delivery", signature)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	store.existingID = "event-1"
	second := deliverGitHubWebhook(app, body, "push", "same-delivery", signature)
	if second.Code != http.StatusAccepted || len(store.events) != 1 {
		t.Fatalf("redelivery status = %d, events = %d, body = %s", second.Code, len(store.events), second.Body.String())
	}
}

func newGitHubWebhookTestServer() (*webhookRouteStore, *echo.Echo) {
	store := newWebhookRouteStore()
	store.sources["github"] = domain.WebhookSource{
		ID: "github", Name: "GitHub", Enabled: true, Provider: "github", TopicPrefix: "webhook.github.",
		SignatureType: domain.WebhookSignatureGitHubSHA256, SignatureSecret: "github-secret",
	}
	app := echo.New()
	RegisterRoutes(app, RouteOptions{Store: store, WebhookBodyLimit: 1 << 20, NewEventID: func() string { return "event-1" }})
	return store, app
}

func deliverGitHubWebhook(app *echo.Echo, body, event, delivery, signature string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/webhook.github", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if event != "" {
		req.Header.Set("X-GitHub-Event", event)
	}
	if delivery != "" {
		req.Header.Set("X-GitHub-Delivery", delivery)
	}
	if signature != "" {
		req.Header.Set("X-Hub-Signature-256", signature)
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func signGitHubBody(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

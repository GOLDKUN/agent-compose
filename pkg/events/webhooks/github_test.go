package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	domain "github.com/chaitin/agent-compose/pkg/model"
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

func TestGitHubWebhookFormPayloadUsesRawBodyForSignature(t *testing.T) {
	payload := `{"action":"opened","issue":{"number":504}}`
	formBody := encodeGitHubForm(payload)

	store, app := newGitHubWebhookTestServer()
	rec := deliverGitHubWebhookWithContentType(
		app,
		formBody,
		"issues",
		"delivery-form",
		signGitHubBody(formBody, "github-secret"),
		"application/x-www-form-urlencoded",
	)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	eventRecord := store.events["event-1"]
	if eventRecord.Topic != "webhook.github.issues" {
		t.Fatalf("topic = %q", eventRecord.Topic)
	}
	var eventPayload map[string]any
	if err := json.Unmarshal([]byte(eventRecord.PayloadJSON), &eventPayload); err != nil {
		t.Fatalf("decode stored payload: %v", err)
	}
	body, ok := eventPayload["body"].(map[string]any)
	if !ok || body["action"] != "opened" {
		t.Fatalf("stored body = %#v", eventPayload["body"])
	}

	store, app = newGitHubWebhookTestServer()
	rec = deliverGitHubWebhookWithContentType(
		app,
		formBody,
		"issues",
		"delivery-form-invalid-signature",
		signGitHubBody(payload, "github-secret"),
		"application/x-www-form-urlencoded",
	)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("decoded-payload signature status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(store.events) != 0 {
		t.Fatalf("stored events = %#v", store.events)
	}
}

func TestGitHubWebhookRejectsInvalidFormPayload(t *testing.T) {
	for _, formBody := range []string{
		"other=value",
		"payload=%7B%7D&payload=%7B%7D",
		"payload=%zz",
	} {
		store, app := newGitHubWebhookTestServer()
		rec := deliverGitHubWebhookWithContentType(
			app,
			formBody,
			"push",
			"delivery-invalid-form",
			signGitHubBody(formBody, "github-secret"),
			"application/x-www-form-urlencoded",
		)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d, response = %s", formBody, rec.Code, rec.Body.String())
		}
		if len(store.events) != 0 {
			t.Fatalf("body %q stored events = %#v", formBody, store.events)
		}
	}
}

func TestLegacyGitHubWebhookRejectsFormPayload(t *testing.T) {
	store := newWebhookRouteStore()
	store.sources["github"] = domain.WebhookSource{
		ID: "github", Name: "Legacy GitHub", Enabled: true, Provider: "github", TopicPrefix: "webhook.github.",
		TokenHash: TokenHash("legacy-token"),
	}
	app := echo.New()
	RegisterRoutes(app, RouteOptions{Store: store, WebhookBodyLimit: 1 << 20, NewEventID: func() string { return "event-1" }})

	formBody := encodeGitHubForm(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/webhook.github", strings.NewReader(formBody))
	req.Header.Set("Authorization", "Bearer legacy-token")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(store.events) != 0 {
		t.Fatalf("stored events = %#v", store.events)
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
	return deliverGitHubWebhookWithContentType(app, body, event, delivery, signature, "application/json")
}

func deliverGitHubWebhookWithContentType(app *echo.Echo, body, event, delivery, signature, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/webhook.github", strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
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

func encodeGitHubForm(payload string) string {
	return url.Values{"payload": {payload}}.Encode()
}

func signGitHubBody(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

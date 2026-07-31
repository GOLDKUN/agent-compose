package webhooks

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	domain "agent-compose/pkg/model"
)

func TestClearingGitHubSecretEnablesUnsignedEventRouting(t *testing.T) {
	store := newWebhookRouteStore()
	store.sources["github"] = domain.WebhookSource{
		ID: "github", Name: "GitHub", Enabled: true, Provider: "github", TopicPrefix: "webhook.github.",
		SignatureType: domain.WebhookSignatureGitHubSHA256, SignatureSecret: "github-secret",
	}
	app := echo.New()
	RegisterRoutes(app, RouteOptions{Store: store, WebhookBodyLimit: 1 << 20, NewEventID: func() string { return "event-1" }})

	put := httptest.NewRequest(http.MethodPut, "/api/webhook-sources/github", strings.NewReader(
		`{"name":"GitHub","enabled":true,"provider":"github","topic_prefix":"webhook.github.","signature_type":"github_sha256","clear_token":true,"clear_signature":true}`,
	))
	putRec := httptest.NewRecorder()
	app.ServeHTTP(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("configure status = %d, body = %s", putRec.Code, putRec.Body.String())
	}
	if store.sources["github"].SignatureSecret != "" {
		t.Fatal("signature secret was not cleared")
	}

	rec := deliverGitHubWebhook(app, `{}`, "push", "delivery-unsigned", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	event := store.events["event-1"]
	if event.Topic != "webhook.github.push" || event.DeliveryID != "delivery-unsigned" {
		t.Fatalf("event = %#v", event)
	}
}

func TestUnsignedGitHubWebhookRequiresConfiguredToken(t *testing.T) {
	store := newWebhookRouteStore()
	store.sources["github"] = domain.WebhookSource{
		ID: "github", Name: "GitHub", Enabled: true, Provider: "github", TopicPrefix: "webhook.github.",
		TokenHash: TokenHash("proxy-token"), SignatureType: domain.WebhookSignatureGitHubSHA256,
	}
	app := echo.New()
	RegisterRoutes(app, RouteOptions{Store: store, WebhookBodyLimit: 1 << 20, NewEventID: func() string { return "event-1" }})

	withoutToken := deliverGitHubWebhook(app, `{}`, "push", "delivery-unsigned", "")
	if withoutToken.Code != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, body = %s", withoutToken.Code, withoutToken.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/webhook.github", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer proxy-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "push")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status with token = %d, body = %s", rec.Code, rec.Body.String())
	}
}

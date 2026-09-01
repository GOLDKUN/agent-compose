package webhooks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

type candidateWebhookStore struct {
	*webhookRouteStore
	candidates []domain.WebhookSource
}

func (s *candidateWebhookStore) ListEnabledWebhookSourcesForTopic(context.Context, string) ([]domain.WebhookSource, error) {
	return append([]domain.WebhookSource(nil), s.candidates...), nil
}

func TestWebhookRejectsInvalidPersistedGitHubConfiguration(t *testing.T) {
	store := &candidateWebhookStore{
		webhookRouteStore: newWebhookRouteStore(),
		candidates: []domain.WebhookSource{{
			ID: "legacy", Enabled: true, Provider: "github", TopicPrefix: "webhook.custom.",
			SignatureType: domain.WebhookSignatureGitHubSHA256, SignatureSecret: "github-secret",
		}},
	}
	app := echo.New()
	RegisterRoutes(app, RouteOptions{Store: store, WebhookBodyLimit: 1 << 20})
	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/webhook.custom", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signGitHubBody(body, "github-secret"))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError || len(store.events) != 0 {
		t.Fatalf("status = %d, events = %#v, body = %s", rec.Code, store.events, rec.Body.String())
	}
}

func TestUnsignedWebhookRejectsAmbiguousSources(t *testing.T) {
	store := &candidateWebhookStore{
		webhookRouteStore: newWebhookRouteStore(),
		candidates: []domain.WebhookSource{
			{ID: "unsigned", Enabled: true, Provider: "github", TopicPrefix: "webhook.github.", SignatureType: domain.WebhookSignatureGitHubSHA256},
			{ID: "signed", Enabled: true, Provider: "github", TopicPrefix: "webhook.github.", SignatureType: domain.WebhookSignatureGitHubSHA256, SignatureSecret: "secret"},
		},
	}
	app := echo.New()
	RegisterRoutes(app, RouteOptions{Store: store, WebhookBodyLimit: 1 << 20})

	rec := deliverGitHubWebhook(app, `{}`, "push", "delivery-1", "")
	if rec.Code != http.StatusConflict || len(store.events) != 0 {
		t.Fatalf("status = %d, events = %#v, body = %s", rec.Code, store.events, rec.Body.String())
	}
}

func TestWebhookMatchedSourceRetainsDefaultBodyLimit(t *testing.T) {
	store := &candidateWebhookStore{
		webhookRouteStore: newWebhookRouteStore(),
		candidates: []domain.WebhookSource{
			{ID: "default-limit", Enabled: true, TopicPrefix: "webhook.generic.", TokenHash: TokenHash("selected")},
			{ID: "large-limit", Enabled: true, TopicPrefix: "webhook.generic.", TokenHash: TokenHash("other"), BodyLimitBytes: 256},
		},
	}
	app := echo.New()
	RegisterRoutes(app, RouteOptions{Store: store, WebhookBodyLimit: 32})
	body := `{"padding":"` + strings.Repeat("x", 64) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/webhook.generic.push", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer selected")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge || len(store.events) != 0 {
		t.Fatalf("status = %d, events = %#v, body = %s", rec.Code, store.events, rec.Body.String())
	}
}

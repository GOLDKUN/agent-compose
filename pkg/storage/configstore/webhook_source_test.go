package configstore

import (
	"context"
	"testing"

	domain "agent-compose/pkg/model"
)

func TestUpsertWebhookSourceValidatesGitHubSignatureConfiguration(t *testing.T) {
	valid := domain.WebhookSource{
		ID:              "github",
		Provider:        "github",
		TopicPrefix:     "webhook.github.",
		SignatureType:   "github_sha256",
		SignatureSecret: "secret",
	}
	tests := []struct {
		name   string
		mutate func(*domain.WebhookSource)
	}{
		{name: "provider", mutate: func(source *domain.WebhookSource) { source.Provider = "gitlab" }},
		{name: "topic prefix", mutate: func(source *domain.WebhookSource) { source.TopicPrefix = "webhook.other." }},
		{name: "signature secret", mutate: func(source *domain.WebhookSource) { source.SignatureSecret = " " }},
		{name: "case insensitive signature type", mutate: func(source *domain.WebhookSource) {
			source.SignatureType = "GitHub_SHA256"
			source.Provider = "gitlab"
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := FromDB(newMemoryDB(t))
			source := valid
			test.mutate(&source)
			if _, err := store.UpsertWebhookSource(context.Background(), source); err == nil {
				t.Fatal("UpsertWebhookSource returned nil error")
			}
		})
	}

	store := FromDB(newMemoryDB(t))
	if _, err := store.UpsertWebhookSource(context.Background(), valid); err != nil {
		t.Fatalf("UpsertWebhookSource with valid GitHub signature configuration returned error: %v", err)
	}
}

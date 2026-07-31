package model

import (
	"fmt"
	"strings"
)

const WebhookSignatureGitHubSHA256 = "github_sha256"

// GitHubWebhookMode describes whether a webhook source uses legacy generic
// handling, unsigned GitHub event routing, or signed GitHub event routing.
type GitHubWebhookMode int

const (
	GitHubWebhookModeGeneric GitHubWebhookMode = iota
	GitHubWebhookModeUnsigned
	GitHubWebhookModeSHA256
)

// GitHubWebhookModeForSource validates provider-specific configuration and
// reports whether the source uses GitHub event routing and authentication.
// An empty signature type retains the legacy URL-topic and token behavior.
func GitHubWebhookModeForSource(source WebhookSource) (GitHubWebhookMode, error) {
	provider := strings.TrimSpace(source.Provider)
	signatureType := strings.ToLower(strings.TrimSpace(source.SignatureType))
	secret := strings.TrimSpace(source.SignatureSecret)

	if signatureType == "" {
		return GitHubWebhookModeGeneric, nil
	}
	if signatureType != WebhookSignatureGitHubSHA256 {
		if provider == "github" {
			return GitHubWebhookModeGeneric, fmt.Errorf("github webhook source signature type must be %q", WebhookSignatureGitHubSHA256)
		}
		return GitHubWebhookModeGeneric, nil
	}
	if provider != "github" {
		return GitHubWebhookModeGeneric, fmt.Errorf("github sha256 webhook source provider must be github")
	}
	if source.TopicPrefix != "webhook.github." {
		return GitHubWebhookModeGeneric, fmt.Errorf("github webhook source topic prefix must be %q", "webhook.github.")
	}
	if secret == "" {
		return GitHubWebhookModeUnsigned, nil
	}
	return GitHubWebhookModeSHA256, nil
}

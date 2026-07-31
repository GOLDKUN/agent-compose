package model

import (
	"fmt"
	"strings"
)

const (
	WebhookSignatureNone         = "none"
	WebhookSignatureGitHubSHA256 = "github_sha256"
)

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

	if signatureType == WebhookSignatureGitHubSHA256 && provider != "github" {
		return GitHubWebhookModeGeneric, fmt.Errorf("github sha256 webhook source provider must be github")
	}
	if provider != "github" {
		return GitHubWebhookModeGeneric, nil
	}
	if signatureType == "" {
		return GitHubWebhookModeGeneric, nil
	}
	if source.TopicPrefix != "webhook.github." {
		return GitHubWebhookModeGeneric, fmt.Errorf("github webhook source topic prefix must be %q", "webhook.github.")
	}

	switch signatureType {
	case WebhookSignatureNone:
		if secret != "" {
			return GitHubWebhookModeGeneric, fmt.Errorf("unsigned github webhook source signature secret must be empty")
		}
		return GitHubWebhookModeUnsigned, nil
	case WebhookSignatureGitHubSHA256:
		if secret == "" {
			return GitHubWebhookModeGeneric, fmt.Errorf("github sha256 webhook source signature secret is required")
		}
		return GitHubWebhookModeSHA256, nil
	default:
		return GitHubWebhookModeGeneric, fmt.Errorf("github webhook source signature type must be %q or %q", WebhookSignatureNone, WebhookSignatureGitHubSHA256)
	}
}

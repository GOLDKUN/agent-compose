package model

import "testing"

func TestGitHubWebhookModeForSource(t *testing.T) {
	tests := []struct {
		name    string
		source  WebhookSource
		want    GitHubWebhookMode
		wantErr bool
	}{
		{
			name: "signed",
			source: WebhookSource{Provider: "github", TopicPrefix: "webhook.github.",
				SignatureType: WebhookSignatureGitHubSHA256, SignatureSecret: "secret"},
			want: GitHubWebhookModeSHA256,
		},
		{
			name: "unsigned without secret",
			source: WebhookSource{Provider: "github", TopicPrefix: "webhook.github.",
				SignatureType: WebhookSignatureGitHubSHA256},
			want: GitHubWebhookModeUnsigned,
		},
		{
			name: "legacy empty signature type",
			source: WebhookSource{Provider: "github", TopicPrefix: "webhook.github.",
				TokenHash: "hash"},
			want: GitHubWebhookModeGeneric,
		},
		{
			name: "generic none",
			source: WebhookSource{Provider: "generic", TopicPrefix: "webhook.generic.",
				SignatureType: "none"},
			want: GitHubWebhookModeGeneric,
		},
		{
			name: "signed provider",
			source: WebhookSource{Provider: "gitlab", TopicPrefix: "webhook.github.",
				SignatureType: WebhookSignatureGitHubSHA256, SignatureSecret: "secret"},
			wantErr: true,
		},
		{
			name: "signed prefix",
			source: WebhookSource{Provider: "github", TopicPrefix: "webhook.other.",
				SignatureType: WebhookSignatureGitHubSHA256, SignatureSecret: "secret"},
			wantErr: true,
		},
		{
			name: "unknown github signature type",
			source: WebhookSource{Provider: "github", TopicPrefix: "webhook.github.",
				SignatureType: "unknown"},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := GitHubWebhookModeForSource(test.source)
			if test.wantErr {
				if err == nil {
					t.Fatalf("GitHubWebhookModeForSource() = %v, nil; want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("GitHubWebhookModeForSource() = %v, %v; want %v, nil", got, err, test.want)
			}
		})
	}
}

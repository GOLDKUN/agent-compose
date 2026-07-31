package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

func validGitHubSignature(r *http.Request, body []byte, secret string) bool {
	presented := strings.TrimSpace(r.Header.Get("X-Hub-Signature-256"))
	prefix, encoded, ok := strings.Cut(presented, "=")
	if !ok || prefix != "sha256" || len(encoded) != sha256.Size*2 {
		return false
	}
	want, err := hex.DecodeString(encoded)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

func githubTopic(r *http.Request) (string, bool) {
	event := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	if event == "" || strings.ContainsAny(event, ". /\\") {
		return "", false
	}
	topic := "webhook.github." + event
	if ValidateExternalTopic(topic) != nil {
		return "", false
	}
	return topic, true
}

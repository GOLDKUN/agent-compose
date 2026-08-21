package llms

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// NewFacadeTokenRequest describes the token NewFacadeToken mints. It
// deliberately excludes FacadeToken's computed fields (TokenHash,
// TokenFingerprint, IssuedAt, ...) so there's no ambiguity about which
// fields a caller controls.
type NewFacadeTokenRequest struct {
	SandboxID  string
	Model      string
	ProviderID string
	WireAPI    string
	Source     string
	RunID      string
}

func NewFacadeToken(req NewFacadeTokenRequest) (string, FacadeToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", FacadeToken{}, err
	}
	tokenValue := "ac_llm_" + hex.EncodeToString(raw)
	hash, fingerprint := HashFacadeToken(tokenValue)
	now := time.Now().UTC()
	normalizedWireAPI := strings.TrimSpace(req.WireAPI)
	if normalizedWireAPI != "" {
		normalizedWireAPI = NormalizeWireAPI(normalizedWireAPI)
	}
	return tokenValue, FacadeToken{
		SandboxID:        strings.TrimSpace(req.SandboxID),
		TokenHash:        hash,
		TokenFingerprint: fingerprint,
		Model:            strings.TrimSpace(req.Model),
		ProviderID:       strings.TrimSpace(req.ProviderID),
		WireAPI:          normalizedWireAPI,
		Source:           strings.TrimSpace(req.Source),
		RunID:            strings.TrimSpace(req.RunID),
		IssuedAt:         now,
	}, nil
}

func HashFacadeToken(value string) (string, string) {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	hash := hex.EncodeToString(sum[:])
	if len(hash) < 12 {
		return hash, hash
	}
	return hash, hash[:12]
}

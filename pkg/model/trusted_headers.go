package model

import "context"

type trustedHeadersContextKey struct{}

// TrustedHeader is metadata accepted from the deployment's trusted ingress.
// It is request-scoped and must not be persisted with sandbox state.
type TrustedHeader struct {
	Name  string
	Value string
}

// NewContextWithTrustedHeaders stores a private copy of trusted headers in ctx.
func NewContextWithTrustedHeaders(ctx context.Context, items []TrustedHeader) context.Context {
	return context.WithValue(ctx, trustedHeadersContextKey{}, cloneTrustedHeaders(items))
}

// TrustedHeadersFromContext retrieves a copy of the trusted headers.
// Returns nil when no items were stored.
func TrustedHeadersFromContext(ctx context.Context) []TrustedHeader {
	items, _ := ctx.Value(trustedHeadersContextKey{}).([]TrustedHeader)
	return cloneTrustedHeaders(items)
}

func cloneTrustedHeaders(items []TrustedHeader) []TrustedHeader {
	if len(items) == 0 {
		return nil
	}
	return append([]TrustedHeader(nil), items...)
}

package model

import (
	"context"
	"testing"
)

func TestTrustedHeadersContextCopiesValues(t *testing.T) {
	original := []TrustedHeader{{Name: "x-mpi-user-id", Value: "user-1"}}
	ctx := NewContextWithTrustedHeaders(context.Background(), original)
	original[0].Value = "mutated-input"

	first := TrustedHeadersFromContext(ctx)
	if len(first) != 1 || first[0].Value != "user-1" {
		t.Fatalf("trusted headers = %#v", first)
	}
	first[0].Value = "mutated-output"
	second := TrustedHeadersFromContext(ctx)
	if len(second) != 1 || second[0].Value != "user-1" {
		t.Fatalf("stored trusted headers were mutated: %#v", second)
	}
}

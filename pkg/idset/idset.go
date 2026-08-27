// Package idset normalizes collections of domain identifiers.
package idset

import (
	"sort"
	"strings"
)

// Normalize trims IDs, removes empty values and duplicates, and preserves the
// order of the first occurrence. A nil input produces a non-nil empty result.
func Normalize(ids []string) []string {
	normalized := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	return normalized
}

// Canonical returns normalized IDs in lexical order.
func Canonical(ids []string) []string {
	normalized := Normalize(ids)
	sort.Strings(normalized)
	return normalized
}

package configstore

import (
	"context"
	"testing"
)

func TestIntegrationLLMProviderQueriesPropagateDatabaseClosure(t *testing.T) {
	ctx := context.Background()
	db := newMemoryDB(t)
	store := FromDB(db)
	if err := store.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	tests := []struct {
		name  string
		query func() error
	}{
		{name: "any provider", query: func() error { _, err := store.HasLLMProviders(ctx); return err }},
		{name: "provider identity", query: func() error { _, err := store.HasLLMProvider(ctx, "custom"); return err }},
		{name: "enabled providers", query: func() error { _, err := store.ListEnabledLLMProviders(ctx); return err }},
		{name: "enabled models", query: func() error { _, err := store.ListEnabledLLMModels(ctx); return err }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.query(); err == nil {
				t.Fatal("query error = nil, want closed database error")
			}
		})
	}
}

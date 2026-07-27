package configstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	domain "agent-compose/pkg/model"
)

func TestCompareAndSwapLoaderBindingAllowsSingleConcurrentClaim(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "bindings.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store := FromDB(db)
	if err := store.initSchema(ctx); err != nil {
		t.Fatalf("initSchema returned error: %v", err)
	}

	start := make(chan struct{})
	results := make(chan bool, 2)
	errorsCh := make(chan error, 2)
	var wg sync.WaitGroup
	for _, sandboxID := range []string{"sandbox-a", "sandbox-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := store.CompareAndSwapLoaderBinding(ctx, nil, domain.SchedulerBinding{
				SchedulerID:       "loader-1",
				TriggerID:         "trigger-1",
				SandboxID:         sandboxID,
				SandboxConfigHash: "sha256:config",
			})
			errorsCh <- err
			results <- claimed
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("CompareAndSwapLoaderBinding returned error: %v", err)
		}
	}
	claims := 0
	for claimed := range results {
		if claimed {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("successful concurrent claims = %d, want 1", claims)
	}
}

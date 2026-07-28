package app

import (
	"context"
	"errors"
	"fmt"
)

type backgroundComponent struct {
	name     string
	shutdown func(context.Context) error
}

func stopBackgroundComponents(ctx context.Context, components []backgroundComponent, setupErrors ...error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var joined error
	for _, err := range setupErrors {
		if err != nil {
			joined = errors.Join(joined, err)
		}
	}

	results := make(chan error, len(components))
	for _, component := range components {
		go func() {
			if component.shutdown == nil {
				results <- nil
				return
			}
			if err := component.shutdown(ctx); err != nil {
				results <- fmt.Errorf("stop %s: %w", component.name, err)
				return
			}
			results <- nil
		}()
	}
	for range components {
		joined = errors.Join(joined, <-results)
	}
	return joined
}

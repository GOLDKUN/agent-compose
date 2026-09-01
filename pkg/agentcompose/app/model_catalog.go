package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	appconfig "github.com/chaitin/agent-compose/pkg/config"
	"github.com/chaitin/agent-compose/pkg/llms"
)

type modelCatalogStore interface {
	ApplyModelCatalog(context.Context, llms.ModelCatalog) error
}

func loadModelCatalog(ctx context.Context, config *appconfig.Config, store modelCatalogStore) error {
	if config == nil || store == nil {
		return fmt.Errorf("model catalog configuration and store are required")
	}
	path := filepath.Join(config.DataRoot, llms.ModelsCatalogFilename)
	catalog, err := llms.LoadModelCatalog(path, os.LookupEnv)
	if err != nil {
		return err
	}
	if err := store.ApplyModelCatalog(ctx, catalog); err != nil {
		return fmt.Errorf("apply model catalog %s: %w", path, err)
	}
	return nil
}

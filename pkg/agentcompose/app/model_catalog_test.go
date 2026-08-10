package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "agent-compose/pkg/config"
	"agent-compose/pkg/llms"
)

type modelCatalogStoreFake struct {
	catalog llms.ModelCatalog
	err     error
}

func (s *modelCatalogStoreFake) ApplyModelCatalog(_ context.Context, catalog llms.ModelCatalog) error {
	s.catalog = catalog
	return s.err
}

func TestLoadModelCatalogUsesDataRootAndFailsUnresolvedSecrets(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("CATALOG_TEST_KEY", "resolved-key")
	data := `{"default":"custom/literal","providers":{"custom":{"baseUrl":"https://example.test/v1","protocol":"responses","apiKey":"$CATALOG_TEST_KEY"}}}`
	if err := os.WriteFile(filepath.Join(dataRoot, llms.ModelsCatalogFilename), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &modelCatalogStoreFake{}
	if err := loadModelCatalog(context.Background(), &appconfig.Config{DataRoot: dataRoot}, store); err != nil {
		t.Fatal(err)
	}
	if store.catalog.Default != "custom/literal" || *store.catalog.Providers["custom"].APIKey != "resolved-key" {
		t.Fatalf("catalog = %#v", store.catalog)
	}

	t.Setenv("CATALOG_TEST_KEY", "")
	if err := loadModelCatalog(context.Background(), &appconfig.Config{DataRoot: dataRoot}, store); err == nil || !strings.Contains(err.Error(), "CATALOG_TEST_KEY") {
		t.Fatalf("loadModelCatalog error = %v", err)
	}
}

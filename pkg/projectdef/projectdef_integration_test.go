package projectdef

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chaitin/agent-compose/pkg/sources"
)

func TestIntegrationProjectDefinitionFileWorkflow(t *testing.T) {
	testProjectDefinitionFileWorkflow(t)
}

func TestE2EProjectDefinitionFileWorkflow(t *testing.T) {
	testProjectDefinitionFileWorkflow(t)
}

func testProjectDefinitionFileWorkflow(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent-compose.yml")
	definition := []byte("name: demo\nagents:\n  worker:\n    provider: test\n")
	if err := os.WriteFile(path, definition, 0o600); err != nil {
		t.Fatal(err)
	}

	spec, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "demo" {
		t.Fatalf("project name = %q, want demo", spec.Name)
	}

	normalized, err := NormalizeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := normalized.MarshalCanonicalJSON(false)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseCanonicalJSON(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Name != "demo" || len(decoded.Agents) != 1 || decoded.Agents[0].Name != "worker" {
		t.Fatalf("unexpected canonical project: %#v", decoded)
	}

	assertDefinitionValidationDoesNotResolveSources(t)
}

func assertDefinitionValidationDoesNotResolveSources(t *testing.T) {
	t.Helper()
	spec := &ProjectSpec{Name: "demo", Agents: map[string]AgentSpec{
		"worker": {Scheduler: &SchedulerSpec{Script: ScriptSource{Source: sources.Source{Provider: "http", URL: "https://example.invalid/script.sh"}}}},
	}}
	called := false
	err := Validate(spec, NormalizeOptions{
		ResolveScriptURLs: true,
		ScriptSourceResolver: ScriptSourceResolverFunc(func(context.Context, sources.Source) ([]byte, error) {
			called = true
			return nil, errors.New("resolver must not be called")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("Validate resolved a runtime script source")
	}
	normalized, err := Normalize(spec, NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !IsProjectName(normalized.Name) || ProjectNamePattern == "" {
		t.Fatal("project name contract is unavailable")
	}
}

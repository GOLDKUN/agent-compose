package app

import (
	"context"
	"testing"

	"agent-compose/pkg/compose"
	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
)

func TestProjectAgentModelResolverUsesCurrentRevisionAndDaemonDefault(t *testing.T) {
	ctx := context.Background()
	store := newRunSupervisorTestConfigStore(t)
	project, err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "project-model-preview", Name: "model-preview", SourcePath: "/tmp/model-preview/agent-compose.yml", SourceJSON: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := compose.Parse([]byte("name: model-preview\nagents:\n  coder:\n    provider: codex\n"))
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := compose.Normalize(raw, compose.NormalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	specJSON, err := normalized.MarshalCanonicalJSON(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.SaveProjectRevision(ctx, domain.ProjectRevisionRecord{ProjectID: project.ID, SpecHash: "sha256:model-preview", SpecJSON: string(specJSON)}); err != nil {
		t.Fatal(err)
	}
	project, err = store.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	resolver := newProjectAgentModelResolver(&appconfig.Config{
		LLMAPIEndpoint: "https://daemon.example.test/v1",
		LLMAPIProtocol: "responses",
		LLMAPIKey:      "daemon-key",
		LLMModel:       "dev/gpt-5.5",
	}, store)
	resolutions, err := resolver.ResolveProjectAgentModels(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	resolution := resolutions["coder"]
	if resolution.Model != "dev/gpt-5.5" || resolution.Source != "daemon_default" {
		t.Fatalf("resolution = %#v", resolution)
	}
}

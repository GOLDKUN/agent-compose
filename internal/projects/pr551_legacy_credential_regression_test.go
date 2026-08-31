package projects

// Regression reproduction for PR #551 review comment (eetoc):
// persisted legacy revision with authoring-time workspace credential reference
// -> redacted GetProject -> unrelated PatchProject.

import (
	"context"
	"testing"
	"time"

	"github.com/chaitin/agent-compose/pkg/compose"
	appconfig "github.com/chaitin/agent-compose/pkg/config"
	driverpkg "github.com/chaitin/agent-compose/pkg/driver"
	domain "github.com/chaitin/agent-compose/pkg/model"
)

// buildLegacyRevisionSpec constructs the normalized project state that a
// pre-#551 daemon persisted: the workspace token is still the literal
// authoring-time reference "${LEGACY_GIT_TOKEN}".
func buildLegacyRevisionSpec(t *testing.T) *compose.NormalizedProjectSpec {
	t.Helper()
	legacy := &compose.NormalizedProjectSpec{
		Name: "legacy-private",
		Workspaces: map[string]compose.WorkspaceSpec{
			"private": {
				Provider: "git",
				URL:      "https://git.example/private.git",
				Token:    "${LEGACY_GIT_TOKEN}",
			},
		},
		Agents: []compose.NormalizedAgentSpec{
			{
				Name:     "worker",
				Provider: "codex",
				Model:    "old-model",
				Image:    "guest:latest",
				Driver: &compose.NormalizedDriverSpec{
					Name:   driverpkg.RuntimeDriverDocker,
					Docker: &compose.DockerDriverSpec{},
				},
			},
		},
	}
	return legacy
}

func TestPR551LegacyWorkspaceReferenceSurvivesUnrelatedPatch(t *testing.T) {
	ctx := context.Background()
	legacy := buildLegacyRevisionSpec(t)
	legacyHash, err := legacy.Hash()
	if err != nil {
		t.Fatalf("hash legacy fixture: %v", err)
	}
	revisionJSON, err := legacy.MarshalCanonicalJSON(false)
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}

	store := &pr551Store{project: domain.ProjectRecord{
		ID:              "legacy-1",
		Name:            "legacy-private",
		SourcePath:      "/legacy",
		CurrentRevision: 1,
	}}
	store.revision = domain.ProjectRevisionRecord{
		ProjectID: "legacy-1",
		Revision:  1,
		SpecHash:  legacyHash,
		SpecJSON:  string(revisionJSON),
		CreatedAt: time.Now().UTC(),
	}

	controller := NewController(ControllerDependencies{
		Config:  &appconfig.Config{RuntimeDriver: driverpkg.RuntimeDriverDocker},
		Store:   store,
		Images:  pr551NoopImagesBackend{},
		Volumes: pr551NoopVolumeManager{},
	})

	// Simulate the user-facing GetProject response: token redacted to the
	// marker. PatchProject restores it from the persisted revision, then
	// normalizes with SourceCredentialsResolved.
	submittedRaw := "name: legacy-private\nworkspaces:\n  private:\n    provider: git\n    url: https://git.example/private.git\n    token: '********'\nagents:\n  worker:\n    provider: codex\n    model: new-model\n    image: guest:latest\n    driver:\n      docker: {}\n"
	submitted, err := compose.Parse([]byte(submittedRaw))
	if err != nil {
		t.Fatalf("parse submitted patch: %v", err)
	}
	result, err := controller.PatchProject(ctx, PatchRequest{
		Project:                 ProjectRefByName("legacy-private"),
		Spec:                    submitted,
		ExpectedCurrentSpecHash: legacyHash,
	})
	if err != nil {
		t.Fatalf("PatchProject returned error: %v", err)
	}
	if len(result.Issues) > 0 {
		t.Fatalf("PatchProject blocked an unrelated patch on a legacy credential reference: %#v", result.Issues)
	}
}

// --- minimal fake store ---------------------------------------------------

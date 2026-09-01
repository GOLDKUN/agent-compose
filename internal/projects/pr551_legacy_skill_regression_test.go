package projects

// Regression test for PR #551 review comment (eetoc): a project whose
// persisted revision stores skill git credentials as legacy references
// (username already interpolated to a literal by the pre-#551 daemon,
// password/token kept as ${NAME} references) must survive a redacted
// GetProject round-trip followed by an unrelated PatchProject.
import (
	"context"
	"testing"
	"time"

	"github.com/chaitin/agent-compose/pkg/compose"
	appconfig "github.com/chaitin/agent-compose/pkg/config"
	driverpkg "github.com/chaitin/agent-compose/pkg/driver"
	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestPR551SkillLegacyPasswordTokenReferencesSurvivePatch(t *testing.T) {
	ctx := context.Background()
	// Persisted under main: username already interpolated to literal, password/token remain ${NAME}.
	legacy := &compose.NormalizedProjectSpec{
		Name: "legacy-skill",
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
				Skills: []compose.NormalizedSkillSpec{
					{
						Name:     "private-skill",
						Provider: "git",
						URL:      "https://git.example/private-skill.git",
						Username: "skill-user",
						Password: "${LEGACY_SKILL_PASSWORD}",
						Token:    "${LEGACY_SKILL_TOKEN}",
					},
				},
			},
		},
	}
	legacyHash, err := legacy.Hash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	revisionJSON, err := legacy.MarshalCanonicalJSON(false)
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}
	store := &pr551Store{project: domain.ProjectRecord{
		ID: "legacy-skill-1", Name: "legacy-skill", SourcePath: "/legacy-skill", CurrentRevision: 1,
	}}
	store.revision = domain.ProjectRevisionRecord{
		ProjectID: "legacy-skill-1", Revision: 1, SpecHash: legacyHash, SpecJSON: string(revisionJSON), CreatedAt: time.Now().UTC(),
	}
	controller := NewController(ControllerDependencies{
		Config:  &appconfig.Config{RuntimeDriver: driverpkg.RuntimeDriverDocker},
		Store:   store,
		Images:  pr551NoopImagesBackend{},
		Volumes: pr551NoopVolumeManager{},
	})
	// GetProject on PR redacts username/password/token to ********; user submits back.
	submittedRaw := "name: legacy-skill\nagents:\n  worker:\n    provider: codex\n    model: new-model\n    image: guest:latest\n    driver:\n      docker: {}\n    skills:\n      - name: private-skill\n        provider: git\n        url: https://git.example/private-skill.git\n        username: '********'\n        password: '********'\n        token: '********'\n"
	submitted, err := compose.Parse([]byte(submittedRaw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result, err := controller.PatchProject(ctx, PatchRequest{
		Project:                 ProjectRefByName("legacy-skill"),
		Spec:                    submitted,
		ExpectedCurrentSpecHash: legacyHash,
	})
	if err != nil {
		t.Fatalf("PatchProject error: %v", err)
	}
	if len(result.Issues) > 0 {
		t.Fatalf("PatchProject blocked unrelated patch on legacy skill refs: %#v", result.Issues)
	}
}

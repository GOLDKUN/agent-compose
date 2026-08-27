package execution

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
)

func TestWriteAgentSkillsReconcilesManagedProjection(t *testing.T) {
	root := t.TempDir()
	session := &domain.Sandbox{Summary: domain.SandboxSummary{WorkspacePath: filepath.Join(root, "workspace")}}
	skillSource := filepath.Join(root, "source", "pdf")
	if err := os.MkdirAll(skillSource, 0o755); err != nil {
		t.Fatalf("create skill source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillSource, "SKILL.md"), []byte("---\nname: pdf\ndescription: PDF\n---\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	skillsDir := HostAgentSkillsDir(session)
	if err := os.MkdirAll(filepath.Join(skillsDir, "stale"), 0o755); err != nil {
		t.Fatalf("create stale skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "manual.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatalf("write manual file: %v", err)
	}
	if err := writeAgentSkillsManifest(skillsDir, agentSkillsManifest{Names: []string{"stale"}}); err != nil {
		t.Fatalf("write old manifest: %v", err)
	}

	names, err := WriteAgentSkills(context.Background(), &appconfig.Config{}, session, []ResolvedAgentSkill{{Name: "pdf", LocalDir: skillSource}}, nil)
	if err != nil {
		t.Fatalf("WriteAgentSkills returned error: %v", err)
	}
	if len(names) != 1 || names[0] != "pdf" {
		t.Fatalf("names = %#v, want pdf", names)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "pdf", "SKILL.md")); err != nil {
		t.Fatalf("projected skill missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "stale")); !os.IsNotExist(err) {
		t.Fatalf("stale skill still exists or unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "manual.txt")); err != nil {
		t.Fatalf("manual file should remain: %v", err)
	}
	link := filepath.Join(HostSandboxDir(session), "home", ".claude", "skills")
	if target, err := os.Readlink(link); err != nil || target != "../.agents/skills" {
		t.Fatalf("claude skills link target=%q err=%v", target, err)
	}
}

func TestWriteAgentSkillsIgnoresInvalidManifestNames(t *testing.T) {
	root := t.TempDir()
	session := &domain.Sandbox{Summary: domain.SandboxSummary{WorkspacePath: filepath.Join(root, "workspace")}}
	skillsDir := HostAgentSkillsDir(session)
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatalf("create skills dir: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("create outside dir: %v", err)
	}
	if err := writeAgentSkillsManifest(skillsDir, agentSkillsManifest{Names: []string{filepath.Join("..", "..", "..", "outside")}}); err != nil {
		t.Fatalf("write old manifest: %v", err)
	}

	if _, err := WriteAgentSkills(context.Background(), &appconfig.Config{}, session, nil, nil); err != nil {
		t.Fatalf("WriteAgentSkills returned error: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside dir should remain: %v", err)
	}
}

func TestWriteAgentSkillsDoesNotRemoveUserClaudeSkillsWithoutConfiguredSkills(t *testing.T) {
	root := t.TempDir()
	session := &domain.Sandbox{Summary: domain.SandboxSummary{WorkspacePath: filepath.Join(root, "workspace")}}
	userSkill := filepath.Join(HostSandboxDir(session), "home", ".claude", "skills", "user", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userSkill), 0o755); err != nil {
		t.Fatalf("create user claude skill: %v", err)
	}
	if err := os.WriteFile(userSkill, []byte("user skill"), 0o644); err != nil {
		t.Fatalf("write user claude skill: %v", err)
	}

	names, err := WriteAgentSkills(context.Background(), &appconfig.Config{}, session, nil, nil)
	if err != nil {
		t.Fatalf("WriteAgentSkills returned error: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("names = %#v, want none", names)
	}
	if got, err := os.ReadFile(userSkill); err != nil || string(got) != "user skill" {
		t.Fatalf("user claude skill was modified: %q err=%v", got, err)
	}
}

func TestWriteAgentSkillsRejectsUserClaudeSkillsDirectory(t *testing.T) {
	root := t.TempDir()
	session := &domain.Sandbox{Summary: domain.SandboxSummary{WorkspacePath: filepath.Join(root, "workspace")}}
	skillSource := filepath.Join(root, "source", "pdf")
	if err := os.MkdirAll(skillSource, 0o755); err != nil {
		t.Fatalf("create skill source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillSource, "SKILL.md"), []byte("---\nname: pdf\ndescription: PDF\n---\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	userSkill := filepath.Join(HostSandboxDir(session), "home", ".claude", "skills", "user", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userSkill), 0o755); err != nil {
		t.Fatalf("create user claude skill: %v", err)
	}
	if err := os.WriteFile(userSkill, []byte("user skill"), 0o644); err != nil {
		t.Fatalf("write user claude skill: %v", err)
	}

	_, err := WriteAgentSkills(context.Background(), &appconfig.Config{}, session, []ResolvedAgentSkill{{Name: "pdf", LocalDir: skillSource}}, nil)
	if err == nil {
		t.Fatalf("expected WriteAgentSkills to reject user claude skills directory")
	}
	if got, readErr := os.ReadFile(userSkill); readErr != nil || string(got) != "user skill" {
		t.Fatalf("user claude skill was modified: %q err=%v", got, readErr)
	}
}

// No-shared-mount path (k8s - see docs/design/k8s_pod_runtime_driver_design.md
// §2.1): the reconciled skills directory must be pushed to both guest
// locations a mount would otherwise expose it at for free.
func TestWriteAgentSkillsPushesToGuestWhenPresent(t *testing.T) {
	root := t.TempDir()
	session := &domain.Sandbox{Summary: domain.SandboxSummary{WorkspacePath: filepath.Join(root, "workspace")}}
	skillSource := filepath.Join(root, "source", "pdf")
	if err := os.MkdirAll(skillSource, 0o755); err != nil {
		t.Fatalf("create skill source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillSource, "SKILL.md"), []byte("---\nname: pdf\n---\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	config := &appconfig.Config{}

	type push struct {
		hostSrcDir string
		guestDir   string
	}
	var pushes []push
	writer := func(_ context.Context, hostSrcDir, guestDir string) error {
		pushes = append(pushes, push{hostSrcDir: hostSrcDir, guestDir: guestDir})
		return nil
	}

	names, err := WriteAgentSkills(context.Background(), config, session, []ResolvedAgentSkill{{Name: "pdf", LocalDir: skillSource}}, writer)
	if err != nil {
		t.Fatalf("WriteAgentSkills returned error: %v", err)
	}
	if len(names) != 1 || names[0] != "pdf" {
		t.Fatalf("names = %#v, want pdf", names)
	}
	appconfig.ApplyDefaultGuestPaths(config)
	skillsDir := HostAgentSkillsDir(session)
	wantPushes := []push{
		{hostSrcDir: skillsDir, guestDir: filepath.Join(config.GuestHomePath, ".agents", "skills")},
		{hostSrcDir: skillsDir, guestDir: filepath.Join(config.GuestHomePath, ".claude", "skills")},
	}
	if len(pushes) != len(wantPushes) || pushes[0] != wantPushes[0] || pushes[1] != wantPushes[1] {
		t.Fatalf("pushes = %#v, want %#v", pushes, wantPushes)
	}

	// No skills configured: nothing should be pushed.
	pushes = nil
	if _, err := WriteAgentSkills(context.Background(), config, session, nil, writer); err != nil {
		t.Fatalf("WriteAgentSkills (no skills) returned error: %v", err)
	}
	if len(pushes) != 0 {
		t.Fatalf("pushes with no skills = %#v, want none", pushes)
	}
}

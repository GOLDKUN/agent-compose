package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	appconfig "github.com/chaitin/agent-compose/pkg/config"
	domain "github.com/chaitin/agent-compose/pkg/model"
	"github.com/chaitin/agent-compose/pkg/workspaces"
)

const AgentSystemPromptFileName = "system-prompt.txt"
const agentSkillsManifestFileName = ".agent-compose-skills.json"
const claudeSkillsManagedMarkerFileName = ".agent-compose-managed"

type ResolvedAgentSkill struct {
	Name     string `json:"name"`
	LocalDir string `json:"local_dir"`
}

type agentSkillsManifest struct {
	Names []string `json:"names"`
}

// AgentPromptFileRequest describes the daemon and optional guest copies of an
// agent prompt.
type AgentPromptFileRequest struct {
	Config         *appconfig.Config
	Sandbox        *domain.Sandbox
	Agent          string
	Message        string
	WriteGuestFile GuestFileWriterFunc
}

// AgentOutputSchemaFileRequest describes the daemon and optional guest copies
// of an agent output schema.
type AgentOutputSchemaFileRequest struct {
	Config         *appconfig.Config
	Sandbox        *domain.Sandbox
	Agent          string
	SchemaJSON     string
	WriteGuestFile GuestFileWriterFunc
}

func HostAgentSystemPromptPath(session *domain.Sandbox) string {
	if session == nil || strings.TrimSpace(session.Summary.WorkspacePath) == "" {
		return ""
	}
	return filepath.Join(HostSandboxDir(session), "state", "agents", "system-prompts", AgentSystemPromptFileName)
}

func HostAgentSkillsDir(session *domain.Sandbox) string {
	if session == nil || strings.TrimSpace(session.Summary.WorkspacePath) == "" {
		return ""
	}
	return filepath.Join(HostSandboxDir(session), "home", ".agents", "skills")
}

func WriteAgentPromptFile(ctx context.Context, req AgentPromptFileRequest) (string, error) {
	config, session := req.Config, req.Sandbox
	agent, message, writeGuestFile := req.Agent, req.Message, req.WriteGuestFile
	hostSandboxDir := filepath.Dir(session.Summary.WorkspacePath)
	promptDir := filepath.Join(hostSandboxDir, "state", "agents", "prompts")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		return "", fmt.Errorf("create agent prompt dir: %w", err)
	}
	name := fmt.Sprintf("%s-%d.txt", domain.NormalizeAgentKind(agent), time.Now().UTC().UnixNano())
	hostPath := filepath.Join(promptDir, name)
	if err := os.WriteFile(hostPath, []byte(message), 0o644); err != nil {
		return "", fmt.Errorf("write agent prompt file: %w", err)
	}
	guestPath := filepath.Join(config.GuestStateRoot, "agents", "prompts", name)
	// No shared filesystem (k8s - see design doc §2.1): the local write above
	// is daemon-side bookkeeping only, and the guest process reading
	// guestPath needs the content pushed there separately.
	if writeGuestFile != nil {
		if err := writeGuestFile(ctx, guestPath, []byte(message)); err != nil {
			return "", fmt.Errorf("push agent prompt file to guest: %w", err)
		}
	}
	return guestPath, nil
}

func WriteAgentSkills(ctx context.Context, config *appconfig.Config, session *domain.Sandbox, skills []ResolvedAgentSkill, writeGuestDir GuestDirWriterFunc) ([]string, error) {
	skillsDir := HostAgentSkillsDir(session)
	if skillsDir == "" {
		if len(skills) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("session workspace path is required to write agent skills")
	}
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create agent skills dir: %w", err)
	}
	current := make(map[string]ResolvedAgentSkill, len(skills))
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		name := strings.TrimSpace(skill.Name)
		if err := validateAgentSkillName(name); err != nil {
			return nil, err
		}
		localDir := strings.TrimSpace(skill.LocalDir)
		if localDir == "" {
			return nil, fmt.Errorf("agent skill %s local dir is required", name)
		}
		if _, ok := current[name]; ok {
			return nil, fmt.Errorf("duplicate agent skill %s", name)
		}
		current[name] = ResolvedAgentSkill{Name: name, LocalDir: localDir}
		names = append(names, name)
	}
	previous := readAgentSkillsManifest(skillsDir)
	for _, name := range previous.Names {
		if err := validateAgentSkillName(name); err != nil {
			continue
		}
		if _, ok := current[name]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(skillsDir, name)); err != nil {
			return nil, fmt.Errorf("remove stale agent skill %s: %w", name, err)
		}
	}
	for _, name := range names {
		if err := copyAgentSkill(current[name], filepath.Join(skillsDir, name)); err != nil {
			return nil, err
		}
	}
	if err := reconcileClaudeSkillsLink(session, skillsDir, len(names) > 0); err != nil {
		return nil, err
	}
	// No shared filesystem (k8s - see design doc §2.1): push the reconciled
	// skills directory to the guest paths a mount would otherwise expose it
	// at for free. Simplification vs. the host path above: pushed as two
	// independent copies (.agents/skills and .claude/skills) rather than a
	// symlink - the guest has no use for reconcileClaudeSkillsLink's
	// managed-marker/symlink-vs-copy-fallback bookkeeping, since a fresh Pod
	// has no pre-existing content to be careful not to clobber. Still push
	// (skillsDir now near-empty, holding just the manifest) when transitioning
	// from having skills to having none, so a *reused* sandbox's guest copy
	// doesn't keep serving skills the project no longer declares; gated on
	// previously having pushed something so a project with no skills at all
	// never pays for the exec round trip.
	if writeGuestDir != nil && (len(names) > 0 || len(previous.Names) > 0) {
		appconfig.ApplyDefaultGuestPaths(config)
		for _, guestSkillsDir := range []string{
			filepath.Join(config.GuestHomePath, ".agents", "skills"),
			filepath.Join(config.GuestHomePath, ".claude", "skills"),
		} {
			if err := writeGuestDir(ctx, skillsDir, guestSkillsDir); err != nil {
				return nil, fmt.Errorf("push agent skills to guest %s: %w", guestSkillsDir, err)
			}
		}
	}
	// Written only after a successful guest push: previous.Names above (read
	// from this same file) is what gates that push on the next call, so
	// committing it before the push could succeed would let a transient push
	// failure permanently disable the "tell a reused sandbox's guest side
	// skills were removed" retry - the next call would see an already-empty
	// manifest and skip pushing again.
	if err := writeAgentSkillsManifest(skillsDir, agentSkillsManifest{Names: names}); err != nil {
		return nil, err
	}
	return names, nil
}

func validateAgentSkillName(name string) error {
	if name == "" {
		return fmt.Errorf("agent skill name is required")
	}
	if filepath.IsAbs(name) || name == "." || name == ".." || name != filepath.Base(name) {
		return fmt.Errorf("agent skill name %q is not a valid path segment", name)
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("agent skill name %q is not a valid path segment", name)
	}
	return nil
}

func readAgentSkillsManifest(skillsDir string) agentSkillsManifest {
	data, err := os.ReadFile(filepath.Join(skillsDir, agentSkillsManifestFileName))
	if err != nil {
		return agentSkillsManifest{}
	}
	var manifest agentSkillsManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return agentSkillsManifest{}
	}
	return manifest
}

func writeAgentSkillsManifest(skillsDir string, manifest agentSkillsManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent skills manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(skillsDir, agentSkillsManifestFileName), data, 0o644); err != nil {
		return fmt.Errorf("write agent skills manifest: %w", err)
	}
	return nil
}

func copyAgentSkill(skill ResolvedAgentSkill, dst string) error {
	srcRoot, err := os.OpenRoot(skill.LocalDir)
	if err != nil {
		return fmt.Errorf("open agent skill %s: %w", skill.Name, err)
	}
	defer func() { _ = srcRoot.Close() }()
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("remove agent skill destination %s: %w", skill.Name, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create agent skill destination %s: %w", skill.Name, err)
	}
	if err := workspaces.CopyRootDirectoryContents(srcRoot, dst); err != nil {
		return fmt.Errorf("copy agent skill %s: %w", skill.Name, err)
	}
	return nil
}

func reconcileClaudeSkillsLink(session *domain.Sandbox, skillsDir string, enabled bool) error {
	claudeSkills := filepath.Join(HostSandboxDir(session), "home", ".claude", "skills")
	if !enabled {
		managed, err := managedClaudeSkillsPath(claudeSkills)
		if err != nil {
			return err
		}
		if managed {
			if err := os.RemoveAll(claudeSkills); err != nil {
				return fmt.Errorf("remove managed claude skills path: %w", err)
			}
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(claudeSkills), 0o755); err != nil {
		return fmt.Errorf("create claude skills parent: %w", err)
	}
	managed, err := managedClaudeSkillsPath(claudeSkills)
	if err != nil {
		return err
	}
	if !managed {
		if _, err := os.Lstat(claudeSkills); err == nil {
			return fmt.Errorf("claude skills path %s already exists and is not managed by agent-compose", claudeSkills)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat claude skills path: %w", err)
		}
	}
	if managed {
		if err := os.RemoveAll(claudeSkills); err != nil {
			return fmt.Errorf("remove managed claude skills path: %w", err)
		}
	}
	if err := os.Symlink("../.agents/skills", claudeSkills); err == nil {
		return nil
	}
	srcRoot, err := os.OpenRoot(skillsDir)
	if err != nil {
		return fmt.Errorf("open projected skills for claude fallback: %w", err)
	}
	defer func() { _ = srcRoot.Close() }()
	if err := os.MkdirAll(claudeSkills, 0o755); err != nil {
		return fmt.Errorf("create claude skills fallback: %w", err)
	}
	if err := os.WriteFile(filepath.Join(claudeSkills, claudeSkillsManagedMarkerFileName), []byte("agent-compose\n"), 0o644); err != nil {
		return fmt.Errorf("write claude skills fallback marker: %w", err)
	}
	if err := workspaces.CopyRootDirectoryContents(srcRoot, claudeSkills); err != nil {
		return fmt.Errorf("copy claude skills fallback: %w", err)
	}
	return nil
}

func managedClaudeSkillsPath(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat claude skills path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return false, fmt.Errorf("read claude skills link: %w", err)
		}
		return filepath.Clean(target) == filepath.Clean("../.agents/skills"), nil
	}
	if !info.IsDir() {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(path, claudeSkillsManagedMarkerFileName)); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat claude skills marker: %w", err)
	}
	return false, nil
}

// WriteAgentSystemPromptFile materializes agent identity for the guest runtime at a
// fixed convention path under the sandbox state tree.
func WriteAgentSystemPromptFile(ctx context.Context, config *appconfig.Config, session *domain.Sandbox, systemPrompt string, writeGuestFile GuestFileWriterFunc) error {
	systemPrompt = strings.TrimSpace(systemPrompt)
	hostPath := HostAgentSystemPromptPath(session)
	if hostPath == "" {
		if systemPrompt == "" {
			return nil
		}
		return fmt.Errorf("sandbox workspace path is required to write agent system prompt")
	}
	if systemPrompt == "" {
		_, statErr := os.Stat(hostPath)
		hadExisting := statErr == nil
		// docker/boxlite see the deletion for free through their shared mount.
		// A no-shared-mount guest (k8s) only finds out via this push, so a
		// sandbox reused across runs must still be told the system prompt was
		// cleared - but only if this sandbox ever had one pushed in the first
		// place, so an agent with no system prompt at all doesn't pay for an
		// exec round trip on every prepare call.
		//
		// Push to the guest before removing the host file: hostPath's
		// existence is what hadExisting is derived from on the next call, so
		// if the guest push failed and we'd already deleted hostPath, a
		// retry would see hadExisting=false and skip clearing the guest
		// forever, leaving it stuck with a stale prompt.
		if hadExisting && writeGuestFile != nil {
			appconfig.ApplyDefaultGuestPaths(config)
			guestPath := filepath.Join(config.GuestStateRoot, "agents", "system-prompts", AgentSystemPromptFileName)
			if err := writeGuestFile(ctx, guestPath, nil); err != nil {
				return fmt.Errorf("clear agent system prompt file on guest: %w", err)
			}
		}
		if err := os.Remove(hostPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove agent system prompt file: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return fmt.Errorf("create agent system prompt dir: %w", err)
	}
	if err := os.WriteFile(hostPath, []byte(systemPrompt), 0o644); err != nil {
		return fmt.Errorf("write agent system prompt file: %w", err)
	}
	if writeGuestFile != nil {
		appconfig.ApplyDefaultGuestPaths(config)
		guestPath := filepath.Join(config.GuestStateRoot, "agents", "system-prompts", AgentSystemPromptFileName)
		if err := writeGuestFile(ctx, guestPath, []byte(systemPrompt)); err != nil {
			return fmt.Errorf("push agent system prompt file to guest: %w", err)
		}
	}
	return nil
}

func WriteAgentOutputSchemaFile(ctx context.Context, req AgentOutputSchemaFileRequest) (string, error) {
	config, session := req.Config, req.Sandbox
	agent, schemaJSON, writeGuestFile := req.Agent, req.SchemaJSON, req.WriteGuestFile
	schemaJSON = strings.TrimSpace(schemaJSON)
	if schemaJSON == "" {
		return "", nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(schemaJSON), &decoded); err != nil {
		return "", fmt.Errorf("decode agent output schema json: %w", err)
	}
	if _, ok := decoded.(map[string]any); !ok {
		return "", fmt.Errorf("agent output schema must be a JSON object")
	}
	hostSandboxDir := filepath.Dir(session.Summary.WorkspacePath)
	schemaDir := filepath.Join(hostSandboxDir, "state", "agents", "schemas")
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		return "", fmt.Errorf("create agent schema dir: %w", err)
	}
	name := fmt.Sprintf("%s-%d.json", domain.NormalizeAgentKind(agent), time.Now().UTC().UnixNano())
	hostPath := filepath.Join(schemaDir, name)
	if err := os.WriteFile(hostPath, []byte(schemaJSON), 0o644); err != nil {
		return "", fmt.Errorf("write agent schema file: %w", err)
	}
	guestPath := filepath.Join(config.GuestStateRoot, "agents", "schemas", name)
	if writeGuestFile != nil {
		if err := writeGuestFile(ctx, guestPath, []byte(schemaJSON)); err != nil {
			return "", fmt.Errorf("push agent schema file to guest: %w", err)
		}
	}
	return guestPath, nil
}

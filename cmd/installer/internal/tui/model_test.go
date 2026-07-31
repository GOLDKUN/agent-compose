package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chaitin/agent-compose/cmd/installer/internal/core"
	tea "github.com/charmbracelet/bubbletea"
)

func TestModelSelectsLanguageAndInstallFlow(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	if view := m.View(); !strings.Contains(view, "DECLARATIVE AGENT RUNTIME") || !strings.Contains(view, "|___/") {
		t.Fatalf("wide TUI is missing product banner:\n%s", view)
	}
	press(t, m, "down")
	press(t, m, "enter")
	if m.language != english || m.screen != screenAction {
		t.Fatalf("language screen = %q, %d", m.language, m.screen)
	}
	press(t, m, "enter")
	if m.operation != core.OperationInstall || m.screen != screenForm || len(m.fields) != 8 {
		t.Fatalf("install form = %q, %d, %d fields", m.operation, m.screen, len(m.fields))
	}
	attachTestRelease(t, m, "v1", "ui-v3")
	form := m.View()
	for _, expected := range []string{"Configure installation", "Install directory", "Application version", "Image registry (optional)", "Guest image (optional)", "Pre-pull guest image", "Install web UI", "Web UI version", "Web UI port", "╭", "Tab / ↑↓ move"} {
		if !strings.Contains(form, expected) {
			t.Fatalf("install form missing %q:\n%s", expected, form)
		}
	}
	for _, hidden := range []string{"Image prefix", "Backend image (optional)", "Frontend image (optional)"} {
		if strings.Contains(form, hidden) {
			t.Fatalf("hidden image control %q rendered in TUI:\n%s", hidden, form)
		}
	}
	m.fields[0].input.SetValue("relative")
	press(t, m, "enter")
	if m.err == nil || !strings.Contains(m.View(), "absolute") {
		t.Fatalf("expected path validation in view: %v\n%s", m.err, m.View())
	}
	installDir := t.TempDir()
	m.fields[0].input.SetValue(installDir)
	press(t, m, "enter")
	if m.screen != screenConfirm {
		t.Fatalf("screen = %d, want confirmation", m.screen)
	}
	confirmation := m.View()
	for _, expected := range []string{installDir, "latest", "Image registry: docker.io (default)", "Backend image: registry.example/agent-compose:v1", "Guest image: registry.example/agent-compose-guest:v1", "release default", "Install web UI: No", "Pre-pull guest image: Yes"} {
		if !strings.Contains(confirmation, expected) {
			t.Fatalf("confirmation missing %q:\n%s", expected, confirmation)
		}
	}
	if m.options.RegistrySet {
		t.Fatal("default Registry hint was treated as an explicit image rewrite")
	}
	for _, absent := range []string{"Web UI port", "Frontend image", "Web UI version"} {
		if strings.Contains(confirmation, absent) {
			t.Fatalf("confirmation offered %q without the web UI:\n%s", absent, confirmation)
		}
	}
}

func TestModelReadsRegistryGuestAndFrontendVersion(t *testing.T) {
	m := installForm(t)
	m.field(fieldRegistry).input.SetValue(" registry.example.com ")
	setExplicitImage(t, m, fieldGuestImage, "registry.example/guest:v2")
	m.field(fieldWithUI).on = true
	frontend := m.field(fieldFrontendVersion)
	frontend.choices = []string{"ui-v3", "ui-v3-legacy"}
	frontend.choice = 1
	frontend.followsRelease = false

	press(t, m, "enter")
	if !m.options.RegistrySet || m.options.Registry != "registry.example.com" {
		t.Fatalf("registry = %q, set=%t", m.options.Registry, m.options.RegistrySet)
	}
	if !m.options.GuestImageSet || m.options.GuestImage != "registry.example/guest:v2" {
		t.Fatalf("guest image = %q, set=%t", m.options.GuestImage, m.options.GuestImageSet)
	}
	if !m.options.FrontendVersionSet || m.options.FrontendVersion != "ui-v3-legacy" {
		t.Fatalf("frontend version = %q, set=%t", m.options.FrontendVersion, m.options.FrontendVersionSet)
	}
	confirmation := m.View()
	for _, value := range []string{"registry.example.com", m.options.GuestImage, "ui-v3-legacy"} {
		if !strings.Contains(confirmation, value) {
			t.Fatalf("confirmation missing %q:\n%s", value, confirmation)
		}
	}
}

func TestModelResolvesImagesWhenVersionLosesFocus(t *testing.T) {
	m := installForm(t)
	if got := m.field(fieldGuestImage).input.Value(); got != "registry.example/agent-compose-guest:v1" {
		t.Fatalf("guest image field = %q", got)
	}
	if view := m.View(); !strings.Contains(view, "registry.example/agent-compose-guest:v1") {
		t.Fatalf("initial release image is missing from the form:\n%s", view)
	}

	m.options.BundleDir = makeTUITestBundle(t, "v2", "ui-v4")
	m.focus = indexOfField(t, m, fieldVersion)
	m.focusFields()
	m.field(fieldVersion).input.SetValue("v2")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated != m || cmd == nil || !m.resolving || m.resolvingVersion != "v2" {
		t.Fatalf("version change did not start resolution: resolving=%t version=%q cmd=%v", m.resolving, m.resolvingVersion, cmd)
	}
	if view := m.View(); !strings.Contains(view, "Resolving release v2") {
		t.Fatalf("resolution status is missing:\n%s", view)
	}
	message := cmd()
	m.Update(message)
	view := m.View()
	for _, value := range []string{
		"ui-v4",
		"registry.example/agent-compose-guest:v2",
	} {
		if !strings.Contains(view, value) {
			t.Fatalf("resolved form is missing %q:\n%s", value, view)
		}
	}
}

func TestModelVersionChangePreservesExplicitImage(t *testing.T) {
	m := installForm(t)
	setExplicitImage(t, m, fieldGuestImage, "operator.example/guest:keep")
	m.options.BundleDir = makeTUITestBundle(t, "v2", "ui-v4")
	m.focus = indexOfField(t, m, fieldVersion)
	m.focusFields()
	m.field(fieldVersion).input.SetValue("v2")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("version change did not start resolution")
	}
	m.Update(cmd())

	guest := m.field(fieldGuestImage)
	if got := guest.input.Value(); got != "operator.example/guest:keep" || guest.followsRelease {
		t.Fatalf("guest image = %q, follows release = %t", got, guest.followsRelease)
	}
	frontend := m.field(fieldFrontendVersion)
	if frontend.choices[frontend.choice] != "ui-v4" || !frontend.followsRelease {
		t.Fatalf("frontend choice = %#v, index=%d, follows=%t", frontend.choices, frontend.choice, frontend.followsRelease)
	}
}

func TestModelVersionChangePreservesSupportedExplicitFrontendVersion(t *testing.T) {
	m := installForm(t)
	frontend := m.field(fieldFrontendVersion)
	frontend.choice = 1
	frontend.followsRelease = false
	m.options.BundleDir = makeTUITestBundleWithFrontendVersions(t, "v2", "ui-v2", "ui-v2", "ui-v1-legacy")

	resolveVersionChange(t, m, "v2")

	if got := selectedChoice(frontend); got != "ui-v1-legacy" || frontend.followsRelease {
		t.Fatalf("frontend version = %q, follows release = %t", got, frontend.followsRelease)
	}
	if !m.options.FrontendVersionSet || m.options.FrontendVersion != "ui-v1-legacy" {
		t.Fatalf("frontend option = %q, set = %t", m.options.FrontendVersion, m.options.FrontendVersionSet)
	}
}

func TestModelVersionChangeReplacesUnsupportedExplicitFrontendVersion(t *testing.T) {
	m := installForm(t)
	m.field(fieldWithUI).on = true
	frontend := m.field(fieldFrontendVersion)
	frontend.choice = 1
	frontend.followsRelease = false
	if err := m.readFields(); err != nil {
		t.Fatal(err)
	}
	m.options.BundleDir = makeTUITestBundle(t, "v2", "ui-v2")
	m.field(fieldVersion).input.SetValue("v2")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("version change did not start resolution")
	}
	m.Update(cmd())

	if got := selectedChoice(frontend); got != "ui-v2" || frontend.followsRelease {
		t.Fatalf("frontend version = %q, follows release = %t", got, frontend.followsRelease)
	}
	if !m.options.FrontendVersionSet || m.options.FrontendVersion != "ui-v2" {
		t.Fatalf("frontend option = %q, set = %t", m.options.FrontendVersion, m.options.FrontendVersionSet)
	}
	if m.err != nil || m.screen != screenConfirm {
		t.Fatalf("fallback frontend version did not reach confirmation: screen=%d err=%v", m.screen, m.err)
	}
}

func resolveVersionChange(t *testing.T, m *model, version string) {
	t.Helper()
	m.focus = indexOfField(t, m, fieldVersion)
	m.focusFields()
	m.field(fieldVersion).input.SetValue(version)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("version change did not start resolution")
	}
	m.Update(cmd())
}

func setExplicitImage(t *testing.T, m *model, id fieldID, value string) {
	t.Helper()
	field := m.field(id)
	if field == nil {
		t.Fatalf("image field %d is missing", id)
	}
	field.input.SetValue(value)
	field.followsRelease = false
}

func TestModelStaysInFormWhenReleaseResolutionFails(t *testing.T) {
	m := installForm(t)
	m.options.BundleDir = filepath.Join(t.TempDir(), "missing")
	m.focus = indexOfField(t, m, fieldVersion)
	m.focusFields()
	m.field(fieldVersion).input.SetValue("missing")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("version change did not start resolution")
	}
	m.Update(cmd())
	if m.screen != screenForm || m.err == nil || !strings.Contains(m.View(), "resolve release missing") {
		t.Fatalf("resolution failure did not remain in the form: screen=%d err=%v\n%s", m.screen, m.err, m.View())
	}
}

func TestModelPortFollowsWebUIToggle(t *testing.T) {
	m := installForm(t)
	port := indexOfField(t, m, fieldPort)

	if !m.fieldDisabled(port) {
		t.Fatal("port is editable while the web UI is disabled")
	}
	form := m.View()
	if !strings.Contains(form, "(not enabled)") {
		t.Fatalf("disabled port is not marked in the form:\n%s", form)
	}

	// Tab from the UI toggle must land past both disabled frontend fields.
	m.focus = indexOfField(t, m, fieldWithUI)
	m.moveFocus(1)
	if m.fields[m.focus].id != fieldInstallDir {
		t.Fatalf("focus stopped on field %d, want the first enabled field", m.fields[m.focus].id)
	}

	m.focus = indexOfField(t, m, fieldWithUI)
	press(t, m, "right")
	if m.fieldDisabled(port) {
		t.Fatal("port stayed disabled after enabling the web UI")
	}
	m.moveFocus(1)
	if m.fields[m.focus].id != fieldFrontendVersion {
		t.Fatalf("focus skipped the re-enabled frontend version, landed on %d", m.fields[m.focus].id)
	}

	press(t, m, "enter")
	if !m.options.WithUI || !m.options.WithUISet {
		t.Fatalf("WithUI = %t, WithUISet = %t", m.options.WithUI, m.options.WithUISet)
	}
	if confirmation := m.View(); !strings.Contains(confirmation, "Web UI port") {
		t.Fatalf("confirmation hid the port with the web UI enabled:\n%s", confirmation)
	}
}

func TestModelFrontendVersionChoiceUsesReleaseOrder(t *testing.T) {
	m := installForm(t)
	m.field(fieldWithUI).on = true
	frontend := m.field(fieldFrontendVersion)
	if got := frontend.choices[frontend.choice]; got != "ui-v1" {
		t.Fatalf("default frontend version = %q", got)
	}
	m.focus = indexOfField(t, m, fieldFrontendVersion)
	press(t, m, "right")
	if got := frontend.choices[frontend.choice]; got != "ui-v1-legacy" || frontend.followsRelease {
		t.Fatalf("next frontend version = %q, follows=%t", got, frontend.followsRelease)
	}
	press(t, m, "left")
	if got := frontend.choices[frontend.choice]; got != "ui-v1" {
		t.Fatalf("previous frontend version = %q", got)
	}
}

func TestModelKeepsFrontendVersionChosenBeforeDisablingUI(t *testing.T) {
	m := installForm(t)
	m.field(fieldWithUI).on = true
	m.focus = indexOfField(t, m, fieldFrontendVersion)
	press(t, m, "right")
	m.field(fieldWithUI).on = false
	press(t, m, "enter")
	if m.options.WithUI || !m.options.FrontendVersionSet || m.options.FrontendVersion != "ui-v1-legacy" {
		t.Fatalf("WithUI=%t frontend=%q set=%t", m.options.WithUI, m.options.FrontendVersion, m.options.FrontendVersionSet)
	}
}

func TestModelGuestPullToggleSetsSkip(t *testing.T) {
	m := installForm(t)
	if m.options.SkipGuestPull {
		t.Fatal("guest pull is skipped by default")
	}
	m.focus = indexOfField(t, m, fieldGuestPull)
	press(t, m, " ")
	press(t, m, "enter")
	if !m.options.SkipGuestPull {
		t.Fatal("toggling the guest field did not set SkipGuestPull")
	}
}

// A greyed-out port is not a choice the operator made, so it must not be
// validated, must not override the port already recorded in .env, and must not
// trip the warning meant for a CLI --port that cannot take effect.
func TestModelDisabledPortIsNotTreatedAsAChoice(t *testing.T) {
	m := installForm(t)
	m.field(fieldPort).input.SetValue("not-a-port")

	press(t, m, "enter")
	if m.err != nil {
		t.Fatalf("disabled port was validated: %v", m.err)
	}
	if m.screen != screenConfirm {
		t.Fatalf("screen = %d, want confirmation", m.screen)
	}
	if m.options.PortSet {
		t.Fatal("disabled port was recorded as an explicit choice")
	}
}

func TestModelEnabledPortIsValidated(t *testing.T) {
	m := installForm(t)
	m.field(fieldWithUI).on = true
	m.field(fieldPort).input.SetValue("not-a-port")

	press(t, m, "enter")
	if m.err == nil || m.screen == screenConfirm {
		t.Fatalf("enabled port skipped validation: err=%v screen=%d", m.err, m.screen)
	}

	m.field(fieldPort).input.SetValue("18080")
	press(t, m, "enter")
	if !m.options.PortSet || m.options.Port != 18080 {
		t.Fatalf("PortSet=%t Port=%d", m.options.PortSet, m.options.Port)
	}
}

func installForm(t *testing.T) *model {
	t.Helper()
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	m.options.InstallDir = t.TempDir()
	press(t, m, "down")
	press(t, m, "enter")
	press(t, m, "enter")
	if m.screen != screenForm {
		t.Fatalf("screen = %d, want the install form", m.screen)
	}
	m.field(fieldInstallDir).input.SetValue(m.options.InstallDir)
	attachTestRelease(t, m, "v1", "ui-v1")
	return m
}

func attachTestRelease(t *testing.T, m *model, version, frontendVersion string) {
	t.Helper()
	dir := makeTUITestBundle(t, version, frontendVersion)
	options := m.options
	options.BundleDir = dir
	release, err := m.service.ResolveRelease(m.ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	m.closeRelease()
	m.release = release
	m.releaseVersion = options.Version
	m.syncReleaseImageFields()
	m.options.BundleDir = dir
	t.Cleanup(m.closeRelease)
}

func makeTUITestBundle(t *testing.T, version, frontendVersion string) string {
	t.Helper()
	return makeTUITestBundleWithFrontendVersions(t, version, frontendVersion, frontendVersion, frontendVersion+"-legacy")
}

func makeTUITestBundleWithFrontendVersions(t *testing.T, version, frontendVersion string, frontendVersions ...string) string {
	t.Helper()
	dir := t.TempDir()
	writeTUITestFile(t, filepath.Join(dir, "docker-compose.yml"), "services: {}\n")
	writeTUITestFile(t, filepath.Join(dir, ".env.example"), "AUTH_PASSWORD=\nAUTH_SECRET=\n")
	manifest := "INSTALLER_PAYLOAD_VERSION=1\n" +
		"AGENT_COMPOSE_IMAGE=registry.example/agent-compose:" + version + "\n" +
		"AGENT_COMPOSE_FRONTEND_VERSION=" + frontendVersion + "\n" +
		"AGENT_COMPOSE_FRONTEND_VERSIONS=" + strings.Join(frontendVersions, ",") + "\n" +
		"AGENT_COMPOSE_FRONTEND_IMAGE=registry.example/agent-compose-ui:" + frontendVersion + "\n" +
		"DEFAULT_IMAGE=registry.example/agent-compose-guest:" + version + "\n"
	writeTUITestFile(t, filepath.Join(dir, "images", "manifest.env"), manifest)
	return dir
}

func writeTUITestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func indexOfField(t *testing.T, m *model, id fieldID) int {
	t.Helper()
	for i := range m.fields {
		if m.fields[i].id == id {
			return i
		}
	}
	t.Fatalf("field %d is missing from the form", id)
	return -1
}

func TestModelUsesCompactBrandOnNarrowTerminal(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = updated.(*model)
	view := m.View()
	if !strings.Contains(view, "agent-compose :: installer") {
		t.Fatalf("compact brand missing:\n%s", view)
	}
	if strings.Contains(view, "DECLARATIVE AGENT RUNTIME") {
		t.Fatalf("wide tagline rendered on narrow terminal:\n%s", view)
	}
}

func TestModelUninstallPurgeChoice(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	press(t, m, "enter")
	press(t, m, "down")
	press(t, m, "down")
	press(t, m, "enter")
	if m.operation != core.OperationUninstall || len(m.fields) != 1 {
		t.Fatalf("uninstall form = %q, %d fields", m.operation, len(m.fields))
	}
	press(t, m, "enter")
	if m.screen != screenPurge {
		t.Fatalf("screen = %d, want purge", m.screen)
	}
	press(t, m, "down")
	press(t, m, "enter")
	if !m.options.Purge || m.screen != screenConfirm {
		t.Fatalf("purge = %t, screen = %d", m.options.Purge, m.screen)
	}
}

func TestModelRendersProgressAndResults(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	t.Cleanup(m.cancel)
	m.screen = screenRunning
	updated, _ := m.Update(eventMessage(core.Event{Message: "Pulling images"}))
	m = updated.(*model)
	if !strings.Contains(m.View(), "Pulling images") {
		t.Fatalf("progress missing:\n%s", m.View())
	}
	updated, _ = m.Update(commandOutputMessage("layer downloaded"))
	m = updated.(*model)
	if !strings.Contains(m.View(), "layer downloaded") {
		t.Fatalf("command output missing:\n%s", m.View())
	}
	updated, _ = m.Update(operationResult{result: core.Result{URL: "http://localhost:80", Username: "admin", GeneratedPassword: "secret"}})
	m = updated.(*model)
	if !strings.Contains(m.View(), "http://localhost:80") || !strings.Contains(m.View(), "secret") {
		t.Fatalf("result missing:\n%s", m.View())
	}
}

func TestModelBoundsVisibleCommandOutput(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	m.screen = screenRunning
	m.width, m.height = 120, 20
	for i := range 20 {
		m.appendEvent(fmt.Sprintf("line-%02d", i))
	}
	view := m.View()
	if strings.Contains(view, "line-00") || !strings.Contains(view, "line-19") {
		t.Fatalf("visible output was not bounded to its tail:\n%s", view)
	}
}

func TestModelCancelsRunningOperationBeforeExit(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	m.screen = screenRunning
	updated, command := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if updated != m || command != nil {
		t.Fatal("running cancellation unexpectedly quit the TUI")
	}
	select {
	case <-m.ctx.Done():
	default:
		t.Fatal("running operation context was not cancelled")
	}
	if !strings.Contains(m.View(), "回滚") {
		t.Fatalf("cancellation state missing from view:\n%s", m.View())
	}

	// Cancellation is not instant, so impatient repeats must not stack lines.
	for range 5 {
		m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	}
	if count := strings.Count(m.View(), "正在取消并回滚"); count != 1 {
		t.Fatalf("cancellation notice rendered %d times:\n%s", count, m.View())
	}
}

func TestModelIgnoresKeysWhileOperationIsRunning(t *testing.T) {
	m := newModel(core.Service{}, core.DefaultOptions(), "/tmp/installer")
	m.screen = screenConfirm
	m.cursor = 0

	updated, command := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated != m || command == nil || m.screen != screenRunning {
		t.Fatalf("confirmation did not start operation: screen=%d command=%v", m.screen, command)
	}

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyUp},
		{Type: tea.KeyDown},
		{Type: tea.KeyRunes, Runes: []rune("x")},
	} {
		updated, command = m.Update(key)
		if updated != m || command != nil {
			t.Fatalf("running key %q started another command", key.String())
		}
	}
}

func press(t *testing.T, m *model, key string) {
	t.Helper()
	keyType := tea.KeyRunes
	runes := []rune(key)
	switch key {
	case "enter":
		keyType, runes = tea.KeyEnter, nil
	case "up":
		keyType, runes = tea.KeyUp, nil
	case "down":
		keyType, runes = tea.KeyDown, nil
	case "left":
		keyType, runes = tea.KeyLeft, nil
	case "right":
		keyType, runes = tea.KeyRight, nil
	case " ":
		keyType, runes = tea.KeyRunes, []rune{' '}
	}
	updated, _ := m.Update(tea.KeyMsg{Type: keyType, Runes: runes})
	if updated != m {
		t.Fatal("model pointer changed unexpectedly")
	}
}

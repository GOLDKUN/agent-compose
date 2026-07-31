package tui

import (
	"strconv"
	"strings"

	"github.com/chaitin/agent-compose/cmd/installer/internal/core"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

type fieldID int

const (
	fieldInstallDir fieldID = iota
	fieldVersion
	fieldRegistry
	fieldGuestImage
	fieldGuestPull
	fieldWithUI
	fieldFrontendVersion
	fieldPort
)

// formField is a text input, an on/off toggle, or a release-backed choice.
// Non-text controls and disabled fields cannot be expressed as textinput.Model,
// so the form tracks fields instead of raw inputs.
type formField struct {
	id             fieldID
	toggle         bool
	on             bool
	choiceField    bool
	choices        []string
	choice         int
	followsRelease bool
	input          textinput.Model
}

func newTextField(id fieldID, value string) formField {
	input := textinput.New()
	input.SetValue(value)
	input.Prompt = ""
	input.CharLimit = 512
	return formField{id: id, input: input}
}

func newToggleField(id fieldID, on bool) formField {
	return formField{id: id, toggle: true, on: on}
}

func newChoiceField(id fieldID, choices []string, selected string) formField {
	field := formField{id: id, choiceField: true, choices: append([]string(nil), choices...), followsRelease: true}
	for index, choice := range choices {
		if choice == selected {
			field.choice = index
			break
		}
	}
	return field
}

func newImageField(id fieldID, value string, explicitlySet bool) formField {
	field := newTextField(id, value)
	field.followsRelease = !explicitlySet
	return field
}

func (m *model) buildFields() {
	m.fields = []formField{newTextField(fieldInstallDir, m.options.InstallDir)}
	if m.operation != core.OperationUninstall {
		registry := newTextField(fieldRegistry, m.options.Registry)
		registry.followsRelease = !m.options.RegistrySet
		registry.input.Placeholder = m.text("docker.io（默认）", "docker.io (default)")
		m.fields = append(m.fields,
			newTextField(fieldVersion, m.options.Version),
			registry,
			newImageField(fieldGuestImage, m.options.GuestImage, m.options.GuestImageSet),
			newToggleField(fieldGuestPull, !m.options.SkipGuestPull),
			newToggleField(fieldWithUI, m.options.WithUI),
			newChoiceField(fieldFrontendVersion, nil, m.options.FrontendVersion),
			newTextField(fieldPort, strconv.Itoa(m.options.Port)),
		)
	}
	m.focus = 0
	m.focusFields()
}

func (m *model) field(id fieldID) *formField {
	for i := range m.fields {
		if m.fields[i].id == id {
			return &m.fields[i]
		}
	}
	return nil
}

// fieldDisabled reports fields that are visible but inert. The port only
// matters when the frontend publishes it, so leaving it greyed out shows the
// dependency without the form jumping around as the toggle flips.
func (m *model) fieldDisabled(index int) bool {
	if m.fields[index].id != fieldPort && m.fields[index].id != fieldFrontendVersion {
		return false
	}
	ui := m.field(fieldWithUI)
	return ui != nil && (!ui.on || (m.fields[index].id == fieldFrontendVersion && len(m.fields[index].choices) == 0))
}

func (m *model) focusFields() {
	for i := range m.fields {
		if i == m.focus && !m.fields[i].toggle && !m.fields[i].choiceField {
			m.fields[i].input.Focus()
			continue
		}
		m.fields[i].input.Blur()
	}
}

func (m *model) moveFocus(delta int) {
	for range m.fields {
		m.focus = (m.focus + delta + len(m.fields)) % len(m.fields)
		if !m.fieldDisabled(m.focus) {
			break
		}
	}
	m.focusFields()
}

func (m *model) toggleFocusedField(delta int) bool {
	if m.fields[m.focus].toggle {
		m.fields[m.focus].on = !m.fields[m.focus].on
		// Flipping the UI toggle can disable the field under the cursor only
		// when focus is already elsewhere, so refreshing focus is enough.
		m.focusFields()
		return true
	}
	if m.fields[m.focus].choiceField && len(m.fields[m.focus].choices) > 0 {
		field := &m.fields[m.focus]
		field.choice = (field.choice + delta + len(field.choices)) % len(field.choices)
		field.followsRelease = false
		return true
	}
	return false
}

func (m *model) readFields() error {
	for i, field := range m.fields {
		switch field.id {
		case fieldInstallDir:
			m.options.InstallDir = strings.TrimSpace(field.input.Value())
		case fieldVersion:
			m.options.Version = strings.TrimSpace(field.input.Value())
		case fieldRegistry:
			m.options.Registry = strings.TrimSpace(field.input.Value())
			m.options.RegistrySet = !field.followsRelease
		case fieldGuestImage:
			m.readImageField(i, &m.options.GuestImage, &m.options.GuestImageSet)
		case fieldGuestPull:
			m.options.SkipGuestPull = !field.on
		case fieldWithUI:
			m.options.WithUI = field.on
			m.options.WithUISet = true
		case fieldFrontendVersion:
			if field.followsRelease || len(field.choices) == 0 {
				continue
			}
			m.options.FrontendVersion = field.choices[field.choice]
			m.options.FrontendVersionSet = true
		case fieldPort:
			// A disabled port publishes nothing. Reading it would validate a
			// value the operator cannot even edit, and marking it as set would
			// both override the port recorded in .env and trigger the warning
			// meant for a CLI --port that cannot take effect.
			if m.fieldDisabled(i) {
				continue
			}
			port, err := core.ParsePort(field.input.Value())
			if err != nil {
				return err
			}
			m.options.Port = port
			m.options.PortSet = true
		}
	}
	if m.operation != core.OperationUninstall {
		m.options.InstallerPath = m.installerPath
	}
	return m.options.Validate(m.operation)
}

func (m *model) readImageField(index int, value *string, explicitlySet *bool) {
	field := &m.fields[index]
	trimmed := strings.TrimSpace(field.input.Value())
	if trimmed == "" {
		field.followsRelease = true
	}
	if field.followsRelease {
		*value = ""
		*explicitlySet = false
		return
	}
	*value = trimmed
	*explicitlySet = true
}

func (m *model) fieldLabel(id fieldID) string {
	switch id {
	case fieldInstallDir:
		return m.text("安装目录", "Install directory")
	case fieldVersion:
		return m.text("应用版本", "Application version")
	case fieldRegistry:
		return m.text("镜像 Registry（可选）", "Image registry (optional)")
	case fieldGuestImage:
		return m.text("Guest 镜像（可选）", "Guest image (optional)")
	case fieldGuestPull:
		return m.text("预拉取 guest 镜像", "Pre-pull guest image")
	case fieldWithUI:
		return m.text("安装 Web UI", "Install web UI")
	case fieldFrontendVersion:
		return m.text("Web UI 版本", "Web UI version")
	case fieldPort:
		return m.text("Web UI 端口", "Web UI port")
	}
	return ""
}

func (m *model) renderForm(body *strings.Builder) {
	title, description := m.formHeading()
	body.WriteString(titleStyle.Render(title) + "\n")
	body.WriteString(mutedStyle.Render(description) + "\n\n")
	for i := range m.fields {
		m.renderFormField(body, i)
	}
	if m.resolving {
		body.WriteString(mutedStyle.Render(m.spinner.View()+" "+m.text("正在解析版本 ", "Resolving release ")+m.resolvingVersion) + "\n")
	}
	if m.err != nil {
		body.WriteString(warnStyle.Render("! "+m.err.Error()) + "\n")
	}
}

func (m *model) renderFormField(body *strings.Builder, index int) {
	field := m.fields[index]
	label := m.fieldLabel(field.id)
	focused := index == m.focus
	disabled := m.fieldDisabled(index)

	marker, labelStyle := mutedStyle.Render("○"), lipgloss.NewStyle()
	switch {
	case disabled:
		marker, labelStyle = mutedStyle.Render("·"), mutedStyle
		label += "  " + m.text("(未启用)", "(not enabled)")
	case focused:
		marker, labelStyle = selectedStyle.Render("●"), selectedStyle
	}
	body.WriteString(marker + " " + labelStyle.Render(label) + "\n")

	if field.toggle {
		body.WriteString("  " + m.renderToggle(field.on, focused) + "\n\n")
		return
	}
	if field.choiceField {
		body.WriteString("  " + m.renderChoice(field, focused && !disabled) + "\n\n")
		return
	}
	boxStyle := fieldStyle
	if disabled {
		boxStyle = disabledField
	} else if focused {
		boxStyle = focusedField
	}
	width := m.fieldWidth()
	input := field.input
	input.Width = width - 2
	value := input.View()
	if disabled {
		value = mutedStyle.Render(field.input.Value())
	}
	body.WriteString(boxStyle.Width(width).Render(value) + "\n\n")
}

func (m *model) renderChoice(field formField, focused bool) string {
	if len(field.choices) == 0 {
		return mutedStyle.Render("‹ " + m.text("正在解析...", "resolving...") + " ›")
	}
	value := field.choices[field.choice]
	rendered := "‹ " + value + " ›"
	if focused {
		return selectedStyle.Render(rendered) + mutedStyle.Render("  "+m.text("←→ / 空格切换", "←→ / space selects"))
	}
	return mutedStyle.Render(rendered)
}

func (m *model) renderToggle(on, focused bool) string {
	label := m.text("否", "No")
	if on {
		label = m.text("是", "Yes")
	}
	rendered := "‹ " + label + " ›"
	if focused {
		return selectedStyle.Render(rendered) + mutedStyle.Render("  "+m.text("←→ / 空格 切换", "←→ / space toggles"))
	}
	return mutedStyle.Render(rendered)
}

func (m *model) fieldWidth() int {
	width := m.width - 8
	if width > 72 {
		width = 72
	}
	if width < 16 {
		width = 16
	}
	return width
}

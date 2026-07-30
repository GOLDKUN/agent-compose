package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/chaitin/agent-compose/cmd/installer/internal/core"
	tea "github.com/charmbracelet/bubbletea"
)

type releaseResolvedMessage struct {
	id      uint64
	version string
	release *core.Release
	err     error
}

func (m *model) resolveRelease(version string, continueAfter bool) tea.Cmd {
	version = strings.TrimSpace(version)
	if version == "" {
		m.err = fmt.Errorf("%s", m.text("版本不能为空", "version must not be empty"))
		return nil
	}
	if m.release != nil && m.releaseVersion == version {
		m.syncReleaseImageFields()
		if continueAfter {
			m.showConfirmation()
		}
		return nil
	}
	if m.resolving && m.resolvingVersion == version {
		m.continueAfterResolve = m.continueAfterResolve || continueAfter
		return nil
	}
	if m.resolveCancel != nil {
		m.resolveCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.resolveCancel = cancel
	m.resolveID++
	id := m.resolveID
	m.resolving = true
	m.resolvingVersion = version
	m.continueAfterResolve = continueAfter
	m.err = nil
	options := m.options
	options.Version = version
	service := m.service
	return func() tea.Msg {
		release, err := service.ResolveRelease(ctx, options)
		if err == nil {
			select {
			case <-ctx.Done():
				release.Close()
				return releaseResolvedMessage{id: id, version: version, err: ctx.Err()}
			default:
			}
		}
		return releaseResolvedMessage{id: id, version: version, release: release, err: err}
	}
}

func (m *model) handleResolvedRelease(msg releaseResolvedMessage) (tea.Model, tea.Cmd) {
	if msg.id != m.resolveID {
		if msg.release != nil {
			msg.release.Close()
		}
		return m, nil
	}
	m.resolving = false
	m.resolvingVersion = ""
	m.resolveCancel = nil
	continueAfter := m.continueAfterResolve
	m.continueAfterResolve = false
	if msg.err != nil {
		m.err = fmt.Errorf("%s %s: %w", m.text("无法解析版本", "resolve release"), msg.version, msg.err)
		return m, nil
	}
	m.closeRelease()
	m.release = msg.release
	m.releaseVersion = msg.version
	m.syncReleaseImageFields()
	m.err = nil
	if continueAfter {
		m.showConfirmation()
	}
	return m, nil
}

func (m *model) syncReleaseImageFields() {
	if m.release == nil {
		return
	}
	preview, previewErr := m.service.PreviewImages(m.operation, m.options, m.release)
	guestImage := m.release.Images.Guest
	if previewErr == nil && preview.Guest.Value != "" {
		guestImage = preview.Guest.Value
	}
	for _, item := range []struct {
		id    fieldID
		value string
	}{
		{id: fieldGuestImage, value: guestImage},
	} {
		field := m.field(item.id)
		if field == nil || (!field.followsRelease && strings.TrimSpace(field.input.Value()) != "") {
			continue
		}
		field.input.SetValue(item.value)
		field.followsRelease = true
	}
	if registry := m.field(fieldRegistry); registry != nil && previewErr == nil && strings.TrimSpace(registry.input.Value()) == "" {
		registry.input.SetValue(preview.Registry)
	}
	frontend := m.field(fieldFrontendVersion)
	if frontend == nil {
		return
	}
	selected := m.release.DefaultFrontendVersion
	if previewErr == nil && preview.FrontendVersion != "" {
		selected = preview.FrontendVersion
	}
	frontend.choices = append(frontend.choices[:0], m.release.FrontendVersions...)
	frontend.choice = 0
	for index, version := range frontend.choices {
		if version == selected {
			frontend.choice = index
			break
		}
	}
	frontend.followsRelease = true
}

func (m *model) showConfirmation() {
	preview, err := m.service.PreviewImages(m.operation, m.options, m.release)
	if err != nil {
		m.err = err
		return
	}
	m.preview = preview
	m.err = nil
	m.screen = screenConfirm
}

func (m *model) closeRelease() {
	if m.resolveCancel != nil {
		m.resolveCancel()
		m.resolveCancel = nil
	}
	m.resolveID++
	m.resolving = false
	m.resolvingVersion = ""
	m.continueAfterResolve = false
	if m.release != nil {
		m.release.Close()
	}
	m.release = nil
}

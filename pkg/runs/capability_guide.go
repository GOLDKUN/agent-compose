package runs

import (
	"agent-compose/pkg/capabilities"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sandboxes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// capabilityGuideDeps groups the dependencies writeCapabilityGuide and
// recordCapabilityGuideWarning need to render a guide and report failures.
type capabilityGuideDeps struct {
	Provider capabilities.Provider
	Store    SandboxRuntimeStore
	Streams  *sandboxes.StreamBroker
}

func writeCapabilityGuide(ctx context.Context, deps capabilityGuideDeps, sandbox *domain.Sandbox, capsetIDs []string) {
	ids := capabilities.NormalizeCapsetIDs(capsetIDs)
	if len(ids) == 0 || deps.Provider == nil || sandbox == nil {
		return
	}
	catalogPath := capabilities.SandboxGuidePath(sandbox)
	if catalogPath == "" {
		return
	}
	var b strings.Builder
	rendered := false
	for _, id := range ids {
		guide, err := capabilities.CapabilityGuideForScope(ctx, deps.Provider, capabilities.GuideScopeFromSandbox(sandbox), id)
		if err != nil {
			slog.Warn("capability guide render skipped", "capset", id, "sandbox_id", sandbox.Summary.ID, "error", err)
			recordCapabilityGuideWarning(ctx, deps, sandbox.Summary.ID, fmt.Sprintf("capability guide render skipped for capset %s", id))
			continue
		}
		if rendered {
			b.WriteString("\n\n")
		}
		b.Write(guide)
		rendered = true
	}
	if !rendered {
		return
	}
	content := b.String()
	if preamble := capabilities.GuidePreamble(capabilities.ProxyTarget(deps.Provider)); preamble != "" {
		content = preamble + content
	}
	if err := os.MkdirAll(filepath.Dir(catalogPath), 0o755); err != nil {
		slog.Warn("capability guide dir create failed", "sandbox_id", sandbox.Summary.ID, "error", err)
		recordCapabilityGuideWarning(ctx, deps, sandbox.Summary.ID, "capability guide directory create failed")
		return
	}
	if err := os.WriteFile(catalogPath, []byte(content), 0o644); err != nil {
		slog.Warn("capability guide write failed", "sandbox_id", sandbox.Summary.ID, "error", err)
		recordCapabilityGuideWarning(ctx, deps, sandbox.Summary.ID, "capability guide write failed")
	}
}

func recordCapabilityGuideWarning(ctx context.Context, deps capabilityGuideDeps, sandboxID, message string) {
	if deps.Store == nil || strings.TrimSpace(sandboxID) == "" {
		return
	}
	event := domain.SandboxEvent{
		ID:        uuid.NewString(),
		Type:      "capability.guide.warning",
		Level:     "warning",
		Message:   message,
		CreatedAt: time.Now().UTC(),
	}
	if err := deps.Store.AddEvent(ctx, sandboxID, event); err != nil {
		slog.Warn("capability guide warning event failed", "sandbox_id", sandboxID, "error", err)
		return
	}
	if deps.Streams != nil {
		deps.Streams.PublishEventAdded(sandboxID, event)
	}
}

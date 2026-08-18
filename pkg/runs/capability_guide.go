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

func writeCapabilityGuide(ctx context.Context, provider capabilities.Provider, store SandboxRuntimeStore, streams *sandboxes.StreamBroker, sandbox *domain.Sandbox, capsetIDs []string) {
	ids := capabilities.NormalizeCapsetIDs(capsetIDs)
	if len(ids) == 0 || provider == nil || sandbox == nil {
		return
	}
	catalogPath := capabilities.SandboxGuidePath(sandbox)
	if catalogPath == "" {
		return
	}
	var b strings.Builder
	rendered := false
	for _, id := range ids {
		guide, err := capabilities.CapabilityGuideForScope(ctx, provider, capabilities.GuideScopeFromSandbox(sandbox), id)
		if err != nil {
			slog.Warn("capability guide render skipped", "capset", id, "sandbox_id", sandbox.Summary.ID, "error", err)
			recordCapabilityGuideWarning(ctx, store, streams, sandbox.Summary.ID, fmt.Sprintf("capability guide render skipped for capset %s", id))
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
	if preamble := capabilities.GuidePreamble(capabilities.ProxyTarget(provider)); preamble != "" {
		content = preamble + content
	}
	if err := os.MkdirAll(filepath.Dir(catalogPath), 0o755); err != nil {
		slog.Warn("capability guide dir create failed", "sandbox_id", sandbox.Summary.ID, "error", err)
		recordCapabilityGuideWarning(ctx, store, streams, sandbox.Summary.ID, "capability guide directory create failed")
		return
	}
	if err := os.WriteFile(catalogPath, []byte(content), 0o644); err != nil {
		slog.Warn("capability guide write failed", "sandbox_id", sandbox.Summary.ID, "error", err)
		recordCapabilityGuideWarning(ctx, store, streams, sandbox.Summary.ID, "capability guide write failed")
	}
}

func recordCapabilityGuideWarning(ctx context.Context, store SandboxRuntimeStore, streams *sandboxes.StreamBroker, sandboxID, message string) {
	if store == nil || strings.TrimSpace(sandboxID) == "" {
		return
	}
	event := domain.SandboxEvent{
		ID:        uuid.NewString(),
		Type:      "capability.guide.warning",
		Level:     "warning",
		Message:   message,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.AddEvent(ctx, sandboxID, event); err != nil {
		slog.Warn("capability guide warning event failed", "sandbox_id", sandboxID, "error", err)
		return
	}
	if streams != nil {
		streams.PublishEventAdded(sandboxID, event)
	}
}

package schedulers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

func NormalizeRuntime(runtime string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "", domain.SchedulerRuntimeScheduler:
		return domain.SchedulerRuntimeScheduler, nil
	default:
		return "", fmt.Errorf("unsupported scheduler runtime %q", runtime)
	}
}

func NormalizeTriggerKind(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case domain.SchedulerTriggerKindInterval:
		return domain.SchedulerTriggerKindInterval, nil
	case domain.SchedulerTriggerKindEvent:
		return domain.SchedulerTriggerKindEvent, nil
	case domain.SchedulerTriggerKindTimeout:
		return domain.SchedulerTriggerKindTimeout, nil
	case domain.SchedulerTriggerKindCron:
		return domain.SchedulerTriggerKindCron, nil
	default:
		return "", fmt.Errorf("unsupported scheduler trigger kind %q", kind)
	}
}

func NormalizeSandboxPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", domain.SchedulerSandboxPolicySticky, domain.SchedulerSandboxPolicyReuse:
		return domain.SchedulerSandboxPolicySticky
	case domain.SchedulerSandboxPolicyNew:
		return domain.SchedulerSandboxPolicyNew
	default:
		return domain.SchedulerSandboxPolicySticky
	}
}

func AgentSandboxPolicy(request domain.SchedulerAgentRequest) string {
	return strings.TrimSpace(request.SandboxPolicy)
}

func AgentSandboxEnv(request domain.SchedulerAgentRequest) []domain.SandboxEnvVar {
	return request.SandboxEnv
}

func CommandSandboxPolicy(request domain.SchedulerCommandRequest) string {
	return strings.TrimSpace(request.SandboxPolicy)
}

func CommandSandboxEnv(request domain.SchedulerCommandRequest) []domain.SandboxEnvVar {
	return request.SandboxEnv
}

func NormalizeConcurrencyPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", domain.SchedulerConcurrencyPolicySkip:
		return domain.SchedulerConcurrencyPolicySkip
	case domain.SchedulerConcurrencyPolicyParallel, "allow":
		return domain.SchedulerConcurrencyPolicyParallel
	default:
		return domain.SchedulerConcurrencyPolicySkip
	}
}

func NormalizeRunStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case domain.SchedulerRunStatusRunning:
		return domain.SchedulerRunStatusRunning
	case domain.SchedulerRunStatusSucceeded:
		return domain.SchedulerRunStatusSucceeded
	case domain.SchedulerRunStatusFailed:
		return domain.SchedulerRunStatusFailed
	case domain.SchedulerRunStatusCanceled:
		return domain.SchedulerRunStatusCanceled
	case domain.SchedulerRunStatusSkipped:
		return domain.SchedulerRunStatusSkipped
	default:
		return domain.SchedulerRunStatusRunning
	}
}

func TriggerStableID(kind, topic string, intervalMs int64, callbackSource string, index int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%s|%d", kind, topic, intervalMs, callbackSource, index)))
	return "auto-" + hex.EncodeToString(h[:6])
}

func SourceSHA(script string) string {
	h := sha256.Sum256([]byte(script))
	return hex.EncodeToString(h[:])
}

func TriggerUsesSchedule(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case domain.SchedulerTriggerKindInterval, domain.SchedulerTriggerKindTimeout, domain.SchedulerTriggerKindCron:
		return true
	default:
		return false
	}
}

func TriggerScheduledAt(now time.Time, delayMs int64) time.Time {
	if delayMs <= 0 {
		return time.Time{}
	}
	return now.UTC().Add(time.Duration(delayMs) * time.Millisecond)
}

func DefaultName(now time.Time) string {
	return "Scheduler " + now.UTC().Format("2006-01-02 15:04")
}

func DefaultScript() string {
	return strings.TrimSpace(`function main(payload) {
  const result = {
    status: "ready",
    now: new Date().toISOString(),
    payload: payload ?? null,
  };
  scheduler.log("scheduler ready", result);
  return result;
}

scheduler.interval("heartbeat", function heartbeat() {
  scheduler.log("heartbeat", { at: new Date().toISOString() });
}, 60000);

scheduler.on("agent-compose.session.created", "on-session-created", function onSession(event) {
  scheduler.log("session created", event);
});
`)
}

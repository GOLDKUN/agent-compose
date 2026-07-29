package adapters

import (
	"encoding/json"
	"testing"

	domain "agent-compose/pkg/model"
)

func TestSchedulerEffectiveSandboxConfigRetainsHistoricalHashSchemaKey(t *testing.T) {
	payload, err := json.Marshal(schedulerEffectiveSandboxConfig{SchedulerConfigHash: "sha256:scheduler"})
	if err != nil {
		t.Fatalf("marshal scheduler sandbox config: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode scheduler sandbox config: %v", err)
	}
	if fields["loader_config_hash"] != "sha256:scheduler" {
		t.Fatalf("loader_config_hash = %#v, want frozen scheduler hash", fields["loader_config_hash"])
	}
	if _, exists := fields["scheduler_config_hash"]; exists {
		t.Fatalf("scheduler_config_hash must not replace the frozen hash key: %s", payload)
	}
}

func TestSchedulerRequestSandboxConfigHashIgnoresVolumeMountOrder(t *testing.T) {
	mounts := []domain.SandboxVolumeMount{
		{ID: "volume-data", Type: domain.VolumeMountTypeVolume, Source: "data", Target: "/workspace/data", HostPath: "/volumes/data", VolumeID: "volume-1"},
		{ID: "bind-cache", Type: domain.VolumeMountTypeBind, Source: "./cache", Target: "/workspace/cache", HostPath: "/project/cache", ProjectPath: "/project"},
	}
	first, err := schedulerRequestSandboxConfigHash("sha256:scheduler", domain.SchedulerAgentRequest{}, nil, nil, nil, nil, "docker", "guest:v1", mounts)
	if err != nil {
		t.Fatalf("schedulerRequestSandboxConfigHash returned error: %v", err)
	}
	reordered := []domain.SandboxVolumeMount{mounts[1], mounts[0]}
	second, err := schedulerRequestSandboxConfigHash("sha256:scheduler", domain.SchedulerAgentRequest{}, nil, nil, nil, nil, "docker", "guest:v1", reordered)
	if err != nil {
		t.Fatalf("schedulerRequestSandboxConfigHash reordered returned error: %v", err)
	}
	if second != first {
		t.Fatalf("volume mount ordering changed scheduler sandbox hash: got %q want %q", second, first)
	}
}

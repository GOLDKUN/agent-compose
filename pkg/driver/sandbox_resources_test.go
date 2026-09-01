package driver

import (
	"testing"

	appconfig "github.com/chaitin/agent-compose/pkg/config"
)

func TestConfiguredSandboxResources(t *testing.T) {
	t.Run("defaults nil and zero-valued config", func(t *testing.T) {
		for _, config := range []*appconfig.Config{nil, {}} {
			got := configuredSandboxResources(config)
			if got.CPUs != 4 || got.MemoryMiB != 4096 || got.DiskSizeGB != 6 {
				t.Fatalf("configuredSandboxResources() = %+v, want 4 CPUs, 4096 MiB, 6 GiB", got)
			}
		}
	})

	t.Run("uses configured values", func(t *testing.T) {
		got := configuredSandboxResources(&appconfig.Config{
			SandboxCPUs:       8,
			SandboxMemoryMiB:  16384,
			SandboxDiskSizeGB: 20,
		})
		if got.CPUs != 8 || got.MemoryMiB != 16384 || got.DiskSizeGB != 20 {
			t.Fatalf("configuredSandboxResources() = %+v, want configured values", got)
		}
	})
}

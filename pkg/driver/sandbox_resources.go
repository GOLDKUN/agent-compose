package driver

import appconfig "github.com/chaitin/agent-compose/pkg/config"

type sandboxResources struct {
	CPUs       uint8
	MemoryMiB  uint32
	DiskSizeGB int32
}

func configuredSandboxResources(config *appconfig.Config) sandboxResources {
	resources := sandboxResources{
		CPUs:       appconfig.DefaultSandboxCPUs,
		MemoryMiB:  appconfig.DefaultSandboxMemoryMiB,
		DiskSizeGB: appconfig.DefaultSandboxDiskSizeGB,
	}
	if config == nil {
		return resources
	}
	if config.SandboxCPUs > 0 {
		resources.CPUs = config.SandboxCPUs
	}
	if config.SandboxMemoryMiB > 0 {
		resources.MemoryMiB = config.SandboxMemoryMiB
	}
	if config.SandboxDiskSizeGB > 0 {
		resources.DiskSizeGB = config.SandboxDiskSizeGB
	}
	return resources
}

package driver

import (
	appconfig "agent-compose/pkg/config"
)

func NewBoxliteRuntime(config *appconfig.Config) (SandboxRuntime, error) {
	return newSandboxRuntime(config)
}

func NewDockerRuntime(config *appconfig.Config) (SandboxRuntime, error) {
	return newDockerRuntime(config)
}

func NewMicrosandboxRuntime(config *appconfig.Config) (SandboxRuntime, error) {
	return newMicrosandboxRuntime(config)
}

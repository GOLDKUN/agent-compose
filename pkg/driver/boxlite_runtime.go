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

func NewK8sRuntime(config *appconfig.Config, proxyStateReader ProxyStateReader) (SandboxRuntime, error) {
	return newK8sRuntime(config, proxyStateReader)
}

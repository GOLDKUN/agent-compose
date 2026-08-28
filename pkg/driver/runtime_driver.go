package driver

import (
	appconfig "agent-compose/pkg/config"
	"fmt"
	"strings"
)

const (
	RuntimeDriverBoxlite      = "boxlite"
	RuntimeDriverDocker       = "docker"
	RuntimeDriverMicrosandbox = "microsandbox"
	RuntimeDriverK8s          = "k8s"
)

func resolveRuntimeDriver(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return RuntimeDriverDocker
	case RuntimeDriverBoxlite:
		return RuntimeDriverBoxlite
	case RuntimeDriverDocker, "docker-engine":
		return RuntimeDriverDocker
	case "msb", RuntimeDriverMicrosandbox:
		return RuntimeDriverMicrosandbox
	case RuntimeDriverK8s, "kubernetes", "pod":
		return RuntimeDriverK8s
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func ResolveRuntimeDriver(value string) string {
	return resolveRuntimeDriver(value)
}

func validateRuntimeDriver(value string) error {
	switch resolveRuntimeDriver(value) {
	case RuntimeDriverBoxlite, RuntimeDriverDocker, RuntimeDriverMicrosandbox, RuntimeDriverK8s:
		return nil
	default:
		return fmt.Errorf("unsupported agent-compose runtime driver %q", strings.TrimSpace(value))
	}
}

func ValidateRuntimeDriver(value string) error {
	return validateRuntimeDriver(value)
}

// RuntimeDriverSupportsStoppedRuntimeRetention reports whether a driver can
// stop a sandbox while preserving the same private writable runtime for a
// later resume. Kubernetes Pods have no stopped-but-retained lifecycle state:
// stopping a Pod deletes it, and resume creates a new Pod from the image.
func RuntimeDriverSupportsStoppedRuntimeRetention(driver string) bool {
	return resolveRuntimeDriver(driver) != RuntimeDriverK8s
}

// RuntimeRefPrefix returns the prefix sandboxstore uses to precompute a new
// sandbox's RuntimeRef (and, for every driver, the resulting container/Pod
// name each driver's own naming fallback would otherwise compute) before the
// driver itself has run. The k8s driver's Pod name is deliberately prefixed
// "agent-compose-sandbox-" rather than bare "agent-compose-" so it reads
// distinctly from the daemon's own Deployment-managed Pod name in a plain
// `kubectl get pods` (see k8sRuntime.podName) - this must return the same
// prefix that fallback would, or the precomputed RuntimeRef silently wins
// over it via firstNonEmpty and the k8s-specific naming never takes effect.
func RuntimeRefPrefix(driver string) string {
	if resolveRuntimeDriver(driver) == RuntimeDriverK8s {
		return "agent-compose-sandbox-"
	}
	return "agent-compose-"
}

func resolveSandboxRuntimeDriver(value, fallback string) (string, error) {
	input := value
	if strings.TrimSpace(input) == "" {
		input = fallback
	}
	driver := resolveRuntimeDriver(input)
	if err := validateRuntimeDriver(driver); err != nil {
		return "", err
	}
	return driver, nil
}

func ResolveSandboxRuntimeDriver(value, fallback string) (string, error) {
	return resolveSandboxRuntimeDriver(value, fallback)
}

func defaultGuestImageForDriver(config *appconfig.Config, driver string) string {
	switch resolveRuntimeDriver(driver) {
	case RuntimeDriverMicrosandbox:
		return config.MicrosandboxDefaultImage
	case RuntimeDriverDocker:
		return firstNonEmpty(config.DockerDefaultImage, config.DefaultImage)
	case RuntimeDriverK8s:
		return firstNonEmpty(config.K8sDefaultImage, config.DefaultImage)
	}
	return config.DefaultImage
}

func DefaultGuestImageForDriver(config *appconfig.Config, driver string) string {
	return defaultGuestImageForDriver(config, driver)
}

func runtimeHomeForDriver(config *appconfig.Config, driver string) string {
	switch resolveRuntimeDriver(driver) {
	case RuntimeDriverMicrosandbox:
		return config.MicrosandboxHome
	case RuntimeDriverDocker:
		return config.DockerHome
	case RuntimeDriverK8s:
		return config.K8sHome
	}
	return config.BoxliteHome
}

func RuntimeHomeForDriver(config *appconfig.Config, driver string) string {
	return runtimeHomeForDriver(config, driver)
}

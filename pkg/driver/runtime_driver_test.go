package driver

import (
	appconfig "github.com/chaitin/agent-compose/pkg/config"
	"testing"
)

func TestResolveRuntimeDriverDocker(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "docker", input: "docker", want: RuntimeDriverDocker},
		{name: "docker-engine alias", input: "docker-engine", want: RuntimeDriverDocker},
		{name: "microsandbox alias", input: "msb", want: RuntimeDriverMicrosandbox},
		{name: "k8s", input: "k8s", want: RuntimeDriverK8s},
		{name: "kubernetes alias", input: "kubernetes", want: RuntimeDriverK8s},
		{name: "pod alias", input: "pod", want: RuntimeDriverK8s},
		{name: "default", input: "", want: RuntimeDriverDocker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveRuntimeDriver(tc.input); got != tc.want {
				t.Fatalf("ResolveRuntimeDriver(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidateRuntimeDriverDocker(t *testing.T) {
	if err := ValidateRuntimeDriver(RuntimeDriverDocker); err != nil {
		t.Fatalf("ValidateRuntimeDriver(%q) returned error: %v", RuntimeDriverDocker, err)
	}
}

func TestValidateRuntimeDriverK8s(t *testing.T) {
	if err := ValidateRuntimeDriver(RuntimeDriverK8s); err != nil {
		t.Fatalf("ValidateRuntimeDriver(%q) returned error: %v", RuntimeDriverK8s, err)
	}
}

func TestRuntimeDriverSupportsStoppedRuntimeRetention(t *testing.T) {
	for _, test := range []struct {
		driver string
		want   bool
	}{
		{driver: RuntimeDriverDocker, want: true},
		{driver: RuntimeDriverBoxlite, want: true},
		{driver: RuntimeDriverMicrosandbox, want: true},
		{driver: RuntimeDriverK8s, want: false},
		{driver: "kubernetes", want: false},
	} {
		if got := RuntimeDriverSupportsStoppedRuntimeRetention(test.driver); got != test.want {
			t.Fatalf("RuntimeDriverSupportsStoppedRuntimeRetention(%q) = %v, want %v", test.driver, got, test.want)
		}
	}
}

func TestDriverDefaultsForDocker(t *testing.T) {
	config := &appconfig.Config{
		DefaultImage:       "box-image:latest",
		DockerDefaultImage: "docker-image:latest",
		BoxliteHome:        "/tmp/boxlite",
		DockerHome:         "/tmp/docker",
		MicrosandboxHome:   "/tmp/microsandbox",
	}

	if got := DefaultGuestImageForDriver(config, RuntimeDriverDocker); got != config.DockerDefaultImage {
		t.Fatalf("DefaultGuestImageForDriver(docker) = %q, want %q", got, config.DockerDefaultImage)
	}
	if got := RuntimeHomeForDriver(config, RuntimeDriverDocker); got != config.DockerHome {
		t.Fatalf("RuntimeHomeForDriver(docker) = %q, want %q", got, config.DockerHome)
	}
}

func TestDriverDefaultsForK8s(t *testing.T) {
	config := &appconfig.Config{
		DefaultImage:    "box-image:latest",
		K8sDefaultImage: "k8s-image:latest",
		K8sHome:         "/tmp/k8s",
	}

	if got := DefaultGuestImageForDriver(config, RuntimeDriverK8s); got != config.K8sDefaultImage {
		t.Fatalf("DefaultGuestImageForDriver(k8s) = %q, want %q", got, config.K8sDefaultImage)
	}
	if got := RuntimeHomeForDriver(config, RuntimeDriverK8s); got != config.K8sHome {
		t.Fatalf("RuntimeHomeForDriver(k8s) = %q, want %q", got, config.K8sHome)
	}

	fallback := &appconfig.Config{DefaultImage: "box-image:latest"}
	if got := DefaultGuestImageForDriver(fallback, RuntimeDriverK8s); got != fallback.DefaultImage {
		t.Fatalf("DefaultGuestImageForDriver(k8s) with no K8sDefaultImage = %q, want fallback %q", got, fallback.DefaultImage)
	}
}

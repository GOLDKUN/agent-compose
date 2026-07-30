package core

import (
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

const (
	DefaultInstallDir = "/opt/agent-compose"
	DefaultRepository = "chaitin/agent-compose"
	DefaultVersion    = "latest"
	DefaultPort       = 80
)

type Operation string

const (
	OperationInstall   Operation = "install"
	OperationUpgrade   Operation = "upgrade"
	OperationUninstall Operation = "uninstall"
)

type Options struct {
	InstallDir         string
	Repository         string
	ReleaseBaseURL     string
	Version            string
	Registry           string
	RegistrySet        bool
	ImagePrefix        string
	BackendImage       string
	BackendImageSet    bool
	FrontendImage      string
	FrontendImageSet   bool
	GuestImage         string
	GuestImageSet      bool
	FrontendVersion    string
	FrontendVersionSet bool
	Port               int
	PortSet            bool
	WithUI             bool
	WithUISet          bool
	SkipGuestPull      bool
	NoStart            bool
	Purge              bool
	KVMPath            string
	BundleDir          string
	InstallerPath      string
}

func DefaultOptions() Options {
	return Options{
		InstallDir: DefaultInstallDir,
		Repository: DefaultRepository,
		Version:    DefaultVersion,
		Port:       DefaultPort,
		KVMPath:    "/dev/kvm",
	}
}

func (o Options) Validate(operation Operation) error {
	if operation != OperationInstall && operation != OperationUpgrade && operation != OperationUninstall {
		return fmt.Errorf("unsupported installer operation %q", operation)
	}
	if !filepath.IsAbs(o.InstallDir) {
		return fmt.Errorf("install directory must be absolute: %s", o.InstallDir)
	}
	if strings.TrimSpace(o.Repository) == "" || strings.ContainsAny(o.Repository, "\r\n") {
		return fmt.Errorf("repository must be a non-empty owner/name")
	}
	if operation != OperationUninstall {
		if strings.TrimSpace(o.Version) == "" || strings.ContainsAny(o.Version, "\r\n") {
			return fmt.Errorf("version must not be empty")
		}
		if o.Port < 1 || o.Port > 65535 {
			return fmt.Errorf("port must be between 1 and 65535")
		}
		if o.RegistrySet {
			if err := validateRegistry(o.Registry); err != nil {
				return err
			}
		}
		if o.RegistrySet && strings.TrimSpace(o.ImagePrefix) != "" {
			return fmt.Errorf("registry and legacy image prefix cannot be used together")
		}
		for _, image := range []struct {
			name  string
			value string
			set   bool
		}{
			{name: "backend image", value: o.BackendImage, set: o.BackendImageSet},
			{name: "frontend image", value: o.FrontendImage, set: o.FrontendImageSet},
			{name: "guest image", value: o.GuestImage, set: o.GuestImageSet},
		} {
			if image.set && (strings.TrimSpace(image.value) == "" || strings.ContainsAny(image.value, "\r\n")) {
				return fmt.Errorf("%s must be a non-empty image reference", image.name)
			}
		}
	}
	return nil
}

func validateRegistry(value string) error {
	registry := strings.TrimSpace(value)
	if registry == "" {
		return fmt.Errorf("registry must not be empty")
	}
	if strings.ContainsAny(registry, "/@?#") || strings.Contains(registry, "://") {
		return fmt.Errorf("registry must be a host or host:port without a scheme or path")
	}
	for _, char := range registry {
		if unicode.IsSpace(char) {
			return fmt.Errorf("registry must not contain whitespace")
		}
	}

	host := registry
	if strings.HasPrefix(registry, "[") {
		end := strings.IndexByte(registry, ']')
		if end < 0 || net.ParseIP(registry[1:end]) == nil {
			return fmt.Errorf("registry contains an invalid IPv6 host")
		}
		host = registry[1:end]
		remainder := registry[end+1:]
		if remainder != "" {
			if !strings.HasPrefix(remainder, ":") || !validRegistryPort(remainder[1:]) {
				return fmt.Errorf("registry contains an invalid port")
			}
		}
	} else {
		if strings.Count(registry, ":") > 1 {
			return fmt.Errorf("registry IPv6 hosts must be enclosed in brackets")
		}
		if candidate, port, ok := strings.Cut(registry, ":"); ok {
			host = candidate
			if !validRegistryPort(port) {
				return fmt.Errorf("registry contains an invalid port")
			}
		}
		if net.ParseIP(host) == nil && !validRegistryHostname(host) {
			return fmt.Errorf("registry contains an invalid host")
		}
	}
	if host == "" {
		return fmt.Errorf("registry host must not be empty")
	}
	return nil
}

func validRegistryPort(value string) bool {
	port, err := strconv.Atoi(value)
	return err == nil && port >= 1 && port <= 65535
}

func validRegistryHostname(value string) bool {
	if value == "" || len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for label := range strings.SplitSeq(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func ParsePort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("port must be between 1 and 65535")
	}
	return port, nil
}

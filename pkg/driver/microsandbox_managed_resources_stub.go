//go:build !linux || !cgo || !microsandboxcgo

package driver

import (
	"context"

	appconfig "github.com/chaitin/agent-compose/pkg/config"
)

func ListMicrosandboxManagedResources(context.Context, *appconfig.Config) ([]ManagedRuntimeResource, []string, error) {
	return nil, nil, nil
}

func RemoveMicrosandboxManagedResource(context.Context, *appconfig.Config, ManagedRuntimeResource) error {
	return ErrRuntimeDriverNotCompiled
}

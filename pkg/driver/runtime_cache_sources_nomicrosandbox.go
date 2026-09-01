//go:build !linux || !cgo || !microsandboxcgo

package driver

import (
	"github.com/chaitin/agent-compose/pkg/cache"
	appconfig "github.com/chaitin/agent-compose/pkg/config"
)

func appendMicrosandboxRuntimeCacheSource(sources []cache.Source, _ *appconfig.Config) []cache.Source {
	return sources
}

//go:build !linux || !cgo || !boxlitecgo

package driver

import (
	"github.com/chaitin/agent-compose/pkg/cache"
	appconfig "github.com/chaitin/agent-compose/pkg/config"
)

func appendBoxliteRuntimeCacheSource(sources []cache.Source, _ *appconfig.Config) []cache.Source {
	return sources
}

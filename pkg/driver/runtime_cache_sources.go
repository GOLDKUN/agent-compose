package driver

import (
	"github.com/chaitin/agent-compose/pkg/cache"
	appconfig "github.com/chaitin/agent-compose/pkg/config"
)

func NewRuntimeCacheSources(config *appconfig.Config) []cache.Source {
	var sources []cache.Source
	sources = appendBoxliteRuntimeCacheSource(sources, config)
	sources = appendMicrosandboxRuntimeCacheSource(sources, config)
	return sources
}

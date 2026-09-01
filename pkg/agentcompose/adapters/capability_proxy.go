package adapters

import (
	"context"
	"strings"

	"github.com/chaitin/agent-compose/pkg/capproxy"
	appconfig "github.com/chaitin/agent-compose/pkg/config"
	domain "github.com/chaitin/agent-compose/pkg/model"
)

type CapabilityGatewayStore interface {
	GetCapabilityGateway(ctx context.Context) (domain.CapabilityGatewaySettings, error)
}

func NewCapProxyServer(config *appconfig.Config, gatewayStore CapabilityGatewayStore, sandboxes capproxy.SandboxResolver, targets capproxy.TargetResolver) *capproxy.Server {
	return capproxy.NewServer(capproxy.Config{
		Listen: strings.TrimSpace(config.CapGRPCListen),
		OctoBus: func(ctx context.Context) (string, string, bool) {
			settings, err := gatewayStore.GetCapabilityGateway(ctx)
			if err != nil || strings.TrimSpace(settings.Addr) == "" {
				return "", "", false
			}
			return settings.Addr, settings.Token, true
		},
		Targets: targets,
	}, sandboxes)
}

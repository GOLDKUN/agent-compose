//go:build !k8scompose

package volumes

import (
	"context"
	"fmt"

	appconfig "github.com/chaitin/agent-compose/pkg/config"
	domain "github.com/chaitin/agent-compose/pkg/model"
)

type K8sDriver struct{}

func NewK8sDriver(_ *appconfig.Config) *K8sDriver { return &K8sDriver{} }
func (*K8sDriver) Name() string                   { return domain.VolumeDriverK8s }
func (*K8sDriver) Create(context.Context, domain.VolumeRecord) (domain.VolumeRecord, error) {
	return domain.VolumeRecord{}, fmt.Errorf("agent-compose was built without k8s support; k8s volume driver is unavailable")
}
func (*K8sDriver) Inspect(context.Context, domain.VolumeRecord) (domain.VolumeRecord, error) {
	return domain.VolumeRecord{}, fmt.Errorf("agent-compose was built without k8s support; k8s volume driver is unavailable")
}
func (*K8sDriver) Remove(context.Context, domain.VolumeRecord) error {
	return fmt.Errorf("agent-compose was built without k8s support; k8s volume driver is unavailable")
}
func (*K8sDriver) ResolveMountSource(context.Context, domain.VolumeRecord) (string, error) {
	return "", fmt.Errorf("agent-compose was built without k8s support; k8s volume driver is unavailable")
}

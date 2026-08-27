//go:build !k8scompose

package driver

import (
	appconfig "agent-compose/pkg/config"
	"context"
	"fmt"
)

type k8sRuntime struct{}

func newK8sRuntime(_ *appconfig.Config) (SandboxRuntime, error) {
	return &k8sRuntime{}, nil
}

func (r *k8sRuntime) EnsureSandbox(context.Context, *Sandbox, VMState, ProxyState) (SandboxVMInfo, error) {
	return SandboxVMInfo{}, fmt.Errorf("agent-compose was built without k8s support; k8s runtime is unavailable")
}

func (r *k8sRuntime) StopSandbox(context.Context, *Sandbox, VMState) (bool, error) {
	return false, fmt.Errorf("agent-compose was built without k8s support; k8s runtime is unavailable")
}

func (r *k8sRuntime) RemoveSandbox(context.Context, *Sandbox, VMState) error {
	return fmt.Errorf("agent-compose was built without k8s support; k8s runtime is unavailable")
}

func (r *k8sRuntime) Exec(context.Context, *Sandbox, VMState, ExecSpec) (ExecResult, error) {
	return ExecResult{}, fmt.Errorf("agent-compose was built without k8s support; k8s runtime is unavailable")
}

func (r *k8sRuntime) ExecStream(context.Context, *Sandbox, VMState, ExecSpec, ExecStreamWriter) (ExecResult, error) {
	return ExecResult{}, fmt.Errorf("agent-compose was built without k8s support; k8s runtime is unavailable")
}

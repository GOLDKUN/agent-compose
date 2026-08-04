package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"

	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/sandboxes"
)

type SandboxRuntime interface {
	EnsureSandbox(context.Context, *domain.Sandbox, domain.VMState, domain.ProxyState) (domain.SandboxVMInfo, error)
	StopSandbox(context.Context, *domain.Sandbox, domain.VMState) (bool, error)
	RemoveSandbox(context.Context, *domain.Sandbox, domain.VMState) error
	Exec(context.Context, *domain.Sandbox, domain.VMState, domain.ExecSpec) (domain.ExecResult, error)
	ExecStream(context.Context, *domain.Sandbox, domain.VMState, domain.ExecSpec, domain.ExecStreamWriter) (domain.ExecResult, error)
}

type SandboxStatsRuntime interface {
	Stats(context.Context, *domain.Sandbox, domain.VMState) (domain.SandboxStats, error)
}

type SandboxGracefulStopRuntime interface {
	PrepareSandboxStop(context.Context, *domain.Sandbox, domain.VMState, time.Duration) (sandboxes.StopPreparationResult, error)
}

type RuntimeProvider interface {
	ForDriver(string) (SandboxRuntime, error)
	ForSession(*domain.Sandbox) (SandboxRuntime, error)
}

type runtimeProvider struct {
	config   *appconfig.Config
	runtimes map[string]SandboxRuntime
}

type driverRuntimeAdapter struct {
	runtime    driverpkg.SandboxRuntime
	executions *sandboxExecutions
}

func NewRuntimeProvider(config *appconfig.Config) (RuntimeProvider, error) {
	if config == nil {
		return nil, fmt.Errorf("runtime provider config is required")
	}
	if err := driverpkg.ValidateCompiledRuntimeDriver(config.RuntimeDriver); err != nil {
		return nil, classifyRuntimeProviderError(err)
	}

	boxliteRuntime, err := driverpkg.NewBoxliteRuntime(config)
	if err != nil {
		return nil, err
	}
	dockerRuntime, err := driverpkg.NewDockerRuntime(config)
	if err != nil {
		return nil, err
	}
	microsandboxRuntime, err := driverpkg.NewMicrosandboxRuntime(config)
	if err != nil {
		return nil, err
	}
	executions := newSandboxExecutions()
	return &runtimeProvider{
		config: config,
		runtimes: map[string]SandboxRuntime{
			driverpkg.RuntimeDriverBoxlite:      driverRuntimeAdapter{runtime: boxliteRuntime, executions: executions},
			driverpkg.RuntimeDriverDocker:       driverRuntimeAdapter{runtime: dockerRuntime, executions: executions},
			driverpkg.RuntimeDriverMicrosandbox: driverRuntimeAdapter{runtime: microsandboxRuntime, executions: executions},
		},
	}, nil
}

func (p *runtimeProvider) ForDriver(driver string) (SandboxRuntime, error) {
	driver = driverpkg.ResolveRuntimeDriver(driver)
	if err := driverpkg.ValidateRuntimeDriver(driver); err != nil {
		return nil, err
	}
	if err := driverpkg.ValidateCompiledRuntimeDriver(driver); err != nil {
		return nil, classifyRuntimeProviderError(err)
	}
	runtime, ok := p.runtimes[driver]
	if !ok {
		return nil, fmt.Errorf("agent-compose runtime %q is not configured", driver)
	}
	return runtime, nil
}

func classifyRuntimeProviderError(err error) error {
	if errors.Is(err, driverpkg.ErrRuntimeDriverNotCompiled) {
		return domain.ClassifyError(domain.ErrUnsupported, "", err)
	}
	return err
}

func (p *runtimeProvider) ForSession(session *domain.Sandbox) (SandboxRuntime, error) {
	if session == nil {
		return nil, fmt.Errorf("session is required")
	}
	driver, err := driverpkg.ResolveSandboxRuntimeDriver(session.Summary.Driver, p.config.RuntimeDriver)
	if err != nil {
		return nil, err
	}
	return p.ForDriver(driver)
}

func (r driverRuntimeAdapter) EnsureSandbox(ctx context.Context, session *domain.Sandbox, vmState domain.VMState, proxyState domain.ProxyState) (domain.SandboxVMInfo, error) {
	info, err := r.runtime.EnsureSandbox(ctx, execution.ToDriverSandbox(session), execution.ToDriverVMState(vmState), execution.ToDriverProxyState(proxyState))
	if err != nil {
		return domain.SandboxVMInfo{}, err
	}
	return execution.FromDriverSandboxVMInfo(info), nil
}

func (r driverRuntimeAdapter) StopSandbox(ctx context.Context, session *domain.Sandbox, vmState domain.VMState) (bool, error) {
	if r.executions != nil {
		r.executions.ensureBlocked(session.Summary.ID)
		// Preserve the runtime driver's complete stop deadline. The sandbox stop
		// is the final containment boundary for executions that have not yet
		// returned after cancellation.
		r.executions.cancel(session.Summary.ID)
	}
	missing, err := r.runtime.StopSandbox(ctx, execution.ToDriverSandbox(session), execution.ToDriverVMState(vmState))
	return missing, err
}

func (r driverRuntimeAdapter) RemoveSandbox(ctx context.Context, session *domain.Sandbox, vmState domain.VMState) error {
	return r.runtime.RemoveSandbox(ctx, execution.ToDriverSandbox(session), execution.ToDriverVMState(vmState))
}

func (r driverRuntimeAdapter) Exec(ctx context.Context, session *domain.Sandbox, vmState domain.VMState, spec domain.ExecSpec) (domain.ExecResult, error) {
	execCtx, marked, finish, err := r.beginExecution(ctx, session, spec)
	if err != nil {
		return domain.ExecResult{}, err
	}
	defer finish()
	result, err := r.runtime.Exec(execCtx, execution.ToDriverSandbox(session), execution.ToDriverVMState(vmState), execution.ToDriverExecSpec(marked))
	return execution.FromDriverExecResult(result), classifyExecTerminationError(err)
}

func (r driverRuntimeAdapter) ExecStream(ctx context.Context, session *domain.Sandbox, vmState domain.VMState, spec domain.ExecSpec, stream domain.ExecStreamWriter) (domain.ExecResult, error) {
	execCtx, marked, finish, err := r.beginExecution(ctx, session, spec)
	if err != nil {
		return domain.ExecResult{}, err
	}
	defer finish()
	driverStream := func(chunk driverpkg.ExecChunk) {
		if stream != nil {
			stream(domain.ExecChunk{Text: chunk.Text, Stream: domainStreamFromDriver(chunk.Stream)})
		}
	}
	result, err := r.runtime.ExecStream(execCtx, execution.ToDriverSandbox(session), execution.ToDriverVMState(vmState), execution.ToDriverExecSpec(marked), driverStream)
	return execution.FromDriverExecResult(result), classifyExecTerminationError(err)
}

func classifyExecTerminationError(err error) error {
	if errors.Is(err, driverpkg.ErrExecTerminationUnconfirmed) {
		return domain.ClassifyError(domain.ErrExecTerminationUnconfirmed, "", err)
	}
	return err
}

func (r driverRuntimeAdapter) OpenInteraction(ctx context.Context, session *domain.Sandbox, vmState domain.VMState, spec driverpkg.RuntimeStartSpec) (driverpkg.RuntimeInteraction, error) {
	interactor, ok := r.runtime.(driverpkg.RuntimeInteractor)
	if !ok {
		return driverpkg.UnsupportedRuntimeInteraction(vmState.Driver, driverpkg.RuntimeInteractionCapabilities{}, spec)
	}
	execCtx, marked, finish, err := r.beginInteraction(ctx, session, spec)
	if err != nil {
		return nil, err
	}
	interaction, err := interactor.OpenInteraction(execCtx, execution.ToDriverSandbox(session), execution.ToDriverVMState(vmState), marked)
	if err != nil {
		finish()
		return nil, err
	}
	tracked := &trackedRuntimeInteraction{
		RuntimeInteraction: interaction,
		finish:             finish,
		done:               make(chan struct{}),
	}
	go tracked.finishWhenContextEnds(execCtx)
	return tracked, nil
}

func domainStreamFromDriver(stream driverpkg.StdioStream) domain.StdioStream {
	if driverpkg.NormalizeStdioStream(stream) == driverpkg.StdioStderr {
		return domain.StdioStderr
	}
	return domain.StdioStdout
}

func (r driverRuntimeAdapter) Stats(ctx context.Context, session *domain.Sandbox, vmState domain.VMState) (domain.SandboxStats, error) {
	statsRuntime, ok := r.runtime.(interface {
		Stats(context.Context, *driverpkg.Sandbox, driverpkg.VMState) (driverpkg.SandboxStats, error)
	})
	if !ok {
		return domain.SandboxStats{}, domain.ClassifyError(domain.ErrUnsupported, "sandbox stats are unsupported by this runtime driver", nil)
	}
	stats, err := statsRuntime.Stats(ctx, execution.ToDriverSandbox(session), execution.ToDriverVMState(vmState))
	return execution.FromDriverSandboxStats(stats), err
}

func (r driverRuntimeAdapter) IsSandboxAlive(ctx context.Context, session *domain.Sandbox, vmState domain.VMState) (bool, error) {
	aliveRuntime, ok := r.runtime.(interface {
		IsSandboxAlive(context.Context, *driverpkg.Sandbox, driverpkg.VMState) (bool, error)
	})
	if !ok {
		return false, domain.ClassifyError(domain.ErrUnsupported, "runtime does not support sandbox liveness checks", nil)
	}
	return aliveRuntime.IsSandboxAlive(ctx, execution.ToDriverSandbox(session), execution.ToDriverVMState(vmState))
}

package sandboxes

import (
	appconfig "agent-compose/pkg/config"
	driverpkg "agent-compose/pkg/driver"
	"agent-compose/pkg/execution"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/workspaces"
	"context"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
)

type LifecycleStore interface {
	GetSandbox(context.Context, string) (*domain.Sandbox, error)
	UpdateSandbox(context.Context, *domain.Sandbox) error
	GetVMState(string) (domain.VMState, error)
	SaveVMState(string, domain.VMState) error
	GetProxyState(string) (domain.ProxyState, error)
	AddEvent(context.Context, string, domain.SandboxEvent) error
}

type SandboxDriver interface {
	StartSandboxVM(context.Context, *domain.Sandbox) error
	StopSandboxVM(context.Context, *domain.Sandbox) error
}

type SandboxRuntimeValidator interface {
	ValidateSandboxRuntime(*domain.Sandbox) error
}

type RuntimeLivenessProvider interface {
	IsSandboxAlive(context.Context, string, *domain.Sandbox, domain.VMState) (bool, bool, error)
}

type FacadeTokenRevoker interface {
	RevokeLLMFacadeTokensForSandbox(context.Context, string) error
}

// SandboxAccessRevoker withdraws in-memory access associated with a sandbox
// after its runtime has stopped or become unreachable.
type SandboxAccessRevoker interface {
	RevokeSandbox(string)
}

type LifecycleNotifier interface {
	PublishSandboxUpdated(*domain.SandboxSummary)
	PublishEventAdded(string, domain.SandboxEvent)
	NotifyDashboard(string)
}

type CapabilityGuideWriter func(context.Context, *domain.Sandbox, []string)

type Lifecycle struct {
	Config                  *appconfig.Config
	Store                   LifecycleStore
	Workspace               workspaces.Store
	WorkspaceEnsurer        workspaces.WorkspaceEnsurer
	Driver                  SandboxDriver
	Liveness                RuntimeLivenessProvider
	TokenRevoker            FacadeTokenRevoker
	AccessRevoker           SandboxAccessRevoker
	Notifier                LifecycleNotifier
	GuideWriter             CapabilityGuideWriter
	PrepareAgentEnvironment func(context.Context, *domain.Sandbox) error
	Locks                   *LifecycleLocks
}

func (l Lifecycle) validateSandboxRuntime(session *domain.Sandbox) error {
	validator, ok := l.Driver.(SandboxRuntimeValidator)
	if !ok {
		return nil
	}
	return validator.ValidateSandboxRuntime(session)
}

func (l Lifecycle) ReconcileRuntimeState(ctx context.Context, session *domain.Sandbox) (*domain.Sandbox, error) {
	if session == nil || session.Summary.VMStatus != domain.VMStatusRunning {
		return session, nil
	}
	driver, err := driverpkg.ResolveSandboxRuntimeDriver(session.Summary.Driver, l.Config.RuntimeDriver)
	if err != nil {
		return nil, err
	}
	if driver == driverpkg.RuntimeDriverMicrosandbox {
		proxyState, err := l.Store.GetProxyState(session.Summary.ID)
		if err != nil {
			return nil, err
		}
		if proxyState.Enabled && JupyterTargetReachable(proxyState, 250*time.Millisecond) {
			return session, nil
		}
	}
	if l.Liveness == nil {
		return session, nil
	}
	vmState, err := l.Store.GetVMState(session.Summary.ID)
	if err != nil {
		return nil, err
	}
	alive, ok, err := l.Liveness.IsSandboxAlive(ctx, driver, session, vmState)
	if err != nil {
		return nil, err
	}
	if !ok || alive {
		return session, nil
	}
	now := time.Now().UTC()
	vmState.StoppedAt = now
	vmState.LastError = ""
	vmState.BoxID = ""
	if err := l.Store.SaveVMState(session.Summary.ID, vmState); err != nil {
		return nil, err
	}
	session.Summary.VMStatus = domain.VMStatusStopped
	if err := l.Store.UpdateSandbox(ctx, session); err != nil {
		return nil, err
	}
	if l.TokenRevoker != nil {
		_ = l.TokenRevoker.RevokeLLMFacadeTokensForSandbox(ctx, session.Summary.ID)
	}
	if l.AccessRevoker != nil {
		l.AccessRevoker.RevokeSandbox(session.Summary.ID)
	}
	event := domain.SandboxEvent{
		ID:        uuid.NewString(),
		Type:      "sandbox.runtime_lost",
		Level:     "warn",
		Message:   "sandbox marked stopped after runtime became unreachable",
		CreatedAt: now,
	}
	_ = l.Store.AddEvent(ctx, session.Summary.ID, event)
	if l.Notifier != nil {
		l.Notifier.PublishSandboxUpdated(&session.Summary)
		l.Notifier.NotifyDashboard("sandbox_updated")
		l.Notifier.PublishEventAdded(session.Summary.ID, event)
	}
	return l.Store.GetSandbox(ctx, session.Summary.ID)
}

func (l Lifecycle) EnsureProxyReady(ctx context.Context, sessionID string) (*domain.Sandbox, domain.ProxyState, error) {
	unlock := l.Locks.Lock(sessionID)
	defer unlock()
	session, err := l.Store.GetSandbox(ctx, sessionID)
	if err != nil {
		return nil, domain.ProxyState{}, err
	}
	if session.Summary.VMStatus == domain.VMStatusDeleting {
		return nil, domain.ProxyState{}, fmt.Errorf("sandbox %s is being deleted", session.Summary.ID)
	}
	proxyState, err := l.Store.GetProxyState(session.Summary.ID)
	if err != nil {
		return nil, domain.ProxyState{}, err
	}
	if !proxyState.Enabled {
		return nil, domain.ProxyState{}, fmt.Errorf("jupyter is not enabled for session %s", session.Summary.ID)
	}
	if session.Summary.VMStatus == domain.VMStatusRunning && JupyterTargetReachable(proxyState, 1500*time.Millisecond) {
		return session, proxyState, nil
	}
	if err := l.validateSandboxRuntime(session); err != nil {
		return nil, domain.ProxyState{}, err
	}
	startCtx, cancel := context.WithTimeout(ctx, l.Config.SandboxStartTimeout)
	defer cancel()
	if err := l.ensureWorkspace(startCtx, session); err != nil {
		session.Summary.VMStatus = domain.VMStatusFailed
		_ = l.Store.UpdateSandbox(ctx, session)
		return nil, domain.ProxyState{}, err
	}
	if err := l.prepareFreshStartAgentEnvironment(startCtx, session); err != nil {
		session.Summary.VMStatus = domain.VMStatusFailed
		_ = l.Store.UpdateSandbox(ctx, session)
		return nil, domain.ProxyState{}, err
	}
	if err := l.Driver.StartSandboxVM(startCtx, session); err != nil {
		session.Summary.VMStatus = domain.VMStatusFailed
		_ = l.Store.UpdateSandbox(ctx, session)
		return nil, domain.ProxyState{}, err
	}
	session.StoppedRuntime = nil
	session.Summary.VMStatus = domain.VMStatusRunning
	if err := l.Store.UpdateSandbox(ctx, session); err != nil {
		return nil, domain.ProxyState{}, err
	}
	loaded, err := l.Store.GetSandbox(ctx, session.Summary.ID)
	if err != nil {
		return nil, domain.ProxyState{}, err
	}
	proxyState, err = l.Store.GetProxyState(session.Summary.ID)
	if err != nil {
		return nil, domain.ProxyState{}, err
	}
	return loaded, proxyState, nil
}

func (l Lifecycle) ResumeLoaded(ctx context.Context, session *domain.Sandbox, capsetIDs []string) (*domain.Sandbox, error) {
	if session == nil {
		return nil, fmt.Errorf("sandbox is being deleted")
	}
	unlock := l.Locks.Lock(session.Summary.ID)
	defer unlock()
	if l.Store != nil {
		current, err := l.Store.GetSandbox(ctx, session.Summary.ID)
		if err != nil {
			return nil, err
		}
		domain.RestoreSandboxTransientFields(current, session)
		session = current
	}
	if session.Summary.VMStatus == domain.VMStatusDeleting {
		return nil, fmt.Errorf("sandbox is being deleted")
	}
	if err := l.validateSandboxRuntime(session); err != nil {
		return nil, err
	}
	if err := l.ensureWorkspace(ctx, session); err != nil {
		return nil, err
	}
	if err := l.prepareFreshStartAgentEnvironment(ctx, session); err != nil {
		session.Summary.VMStatus = domain.VMStatusFailed
		_ = l.Store.UpdateSandbox(ctx, session)
		return nil, err
	}
	if l.GuideWriter != nil {
		l.GuideWriter(ctx, session, capsetIDs)
	}
	if err := l.Driver.StartSandboxVM(ctx, session); err != nil {
		return nil, err
	}
	session.StoppedRuntime = nil
	session.Summary.VMStatus = domain.VMStatusRunning
	if err := l.Store.UpdateSandbox(ctx, session); err != nil {
		return nil, err
	}
	l.publishSandboxUpdated(&session.Summary)
	event := domain.SandboxEvent{
		ID:        uuid.NewString(),
		Type:      "sandbox.resumed",
		Level:     "info",
		Message:   "sandbox resumed with " + session.Summary.Driver + " driver using guest image " + session.Summary.GuestImage,
		CreatedAt: time.Now().UTC(),
	}
	_ = l.Store.AddEvent(ctx, session.Summary.ID, event)
	l.publishEventAdded(session.Summary.ID, event)
	loaded, err := l.Store.GetSandbox(ctx, session.Summary.ID)
	if err != nil {
		return nil, err
	}
	domain.RestoreSandboxTransientFields(loaded, session)
	return loaded, nil
}

func (l Lifecycle) ensureWorkspace(ctx context.Context, session *domain.Sandbox) error {
	if domain.SandboxWorkspaceUnavailable(session) {
		return domain.ClassifyError(domain.ErrFailedPrecondition, "sandbox workspace was reclaimed and cannot be resumed", nil)
	}
	if l.WorkspaceEnsurer == nil {
		return fmt.Errorf("workspace ensurer is not configured")
	}
	return l.WorkspaceEnsurer.Ensure(ctx, session)
}

func (l Lifecycle) prepareFreshStartAgentEnvironment(ctx context.Context, session *domain.Sandbox) error {
	if l.PrepareAgentEnvironment == nil {
		return nil
	}
	vmState, err := l.Store.GetVMState(session.Summary.ID)
	if err != nil {
		return err
	}
	if !vmState.StartedAt.IsZero() && !domain.SandboxRuntimeReleaseIntentional(session) {
		return nil
	}
	return l.PrepareAgentEnvironment(ctx, session)
}

// StopOutcome describes the observable runtime transitions completed by a
// Lifecycle stop operation.
type StopOutcome struct {
	Sandbox         *domain.Sandbox
	DriverStopped   bool
	RuntimeReleased bool
}

// Changed reports whether the driver was stopped or its private runtime was
// released by this operation.
func (o StopOutcome) Changed() bool {
	return o.DriverStopped || o.RuntimeReleased
}

// StopLoaded serializes and performs the complete stop lifecycle for a loaded
// sandbox, including policy application, persistence, events, and access
// revocation.
func (l Lifecycle) StopLoaded(ctx context.Context, session *domain.Sandbox) (StopOutcome, error) {
	if session == nil {
		return StopOutcome{}, fmt.Errorf("sandbox is required")
	}
	if l.Locks == nil {
		return l.stopLoadedWhileLocked(ctx, session)
	}
	unlock := l.Locks.Lock(session.Summary.ID)
	defer unlock()
	return l.stopLoadedWhileLocked(ctx, session)
}

// StopLoadedWhileLocked performs the complete stop lifecycle while the caller
// owns the sandbox lifecycle lock. Callers should normally use StopLoaded.
func (l Lifecycle) StopLoadedWhileLocked(ctx context.Context, session *domain.Sandbox) (StopOutcome, error) {
	if session == nil {
		return StopOutcome{}, fmt.Errorf("sandbox is required")
	}
	return l.stopLoadedWhileLocked(ctx, session)
}

func (l Lifecycle) stopLoadedWhileLocked(ctx context.Context, session *domain.Sandbox) (StopOutcome, error) {
	if l.Store != nil {
		current, err := l.Store.GetSandbox(ctx, session.Summary.ID)
		if err != nil {
			return StopOutcome{}, err
		}
		domain.RestoreSandboxTransientFields(current, session)
		session = current
	}
	if session.Summary.VMStatus == domain.VMStatusDeleting {
		return StopOutcome{}, fmt.Errorf("sandbox is being deleted")
	}
	sandboxRoot := ""
	if l.Config != nil {
		sandboxRoot = l.Config.SandboxRoot
	}
	result, stopErr := StopSandboxRuntime(ctx, sandboxRoot, l.Store, l.Driver, session)
	if result.Stopped || result.Released {
		l.publishSandboxUpdated(&session.Summary)
	}
	for _, event := range result.RuntimeEvents {
		l.publishEventAdded(session.Summary.ID, event)
	}
	if result.Stopped {
		event := domain.SandboxEvent{
			ID:        uuid.NewString(),
			Type:      "sandbox.stopped",
			Level:     "info",
			Message:   SandboxStoppedEventMessage(session, result),
			CreatedAt: time.Now().UTC(),
		}
		_ = l.Store.AddEvent(ctx, session.Summary.ID, event)
		l.publishEventAdded(session.Summary.ID, event)
	}
	outcome := StopOutcome{
		Sandbox:         session,
		DriverStopped:   result.Stopped,
		RuntimeReleased: result.Released,
	}
	if session.Summary.VMStatus == domain.VMStatusStopped && l.AccessRevoker != nil {
		l.AccessRevoker.RevokeSandbox(session.Summary.ID)
	}
	if stopErr != nil {
		return outcome, stopErr
	}
	loaded, err := l.Store.GetSandbox(ctx, session.Summary.ID)
	if err != nil {
		return StopOutcome{}, err
	}
	outcome.Sandbox = loaded
	return outcome, nil
}

type stoppedRuntimeRecoveryStore interface {
	LifecycleStore
	ListSandboxes(context.Context, domain.SandboxListOptions) (domain.SandboxListResult, error)
}

func (l Lifecycle) RecoverStoppedRuntimeReleases(ctx context.Context) []string {
	store, ok := l.Store.(stoppedRuntimeRecoveryStore)
	if !ok {
		return []string{"stopped runtime recovery store does not support sandbox listing"}
	}
	listed, err := store.ListSandboxes(ctx, domain.SandboxListOptions{Limit: 1 << 30})
	if err != nil {
		return []string{fmt.Sprintf("list sandboxes for stopped runtime recovery: %v", err)}
	}
	var warnings []string
	for _, sandbox := range listed.Sandboxes {
		state := domain.EffectiveStoppedRuntimeState(sandbox)
		if domain.EffectiveStoppedRuntimePolicy(sandbox) != domain.StoppedRuntimePolicyRemove ||
			(state != domain.StoppedRuntimeStateReleasePending && state != domain.StoppedRuntimeStateReleased) {
			continue
		}
		if _, err := l.StopLoaded(ctx, sandbox); err != nil {
			warnings = append(warnings, fmt.Sprintf("recover stopped runtime release %s: %v", sandbox.Summary.ID, err))
		}
	}
	return warnings
}

func (l Lifecycle) publishSandboxUpdated(summary *domain.SandboxSummary) {
	if l.Notifier == nil {
		return
	}
	l.Notifier.PublishSandboxUpdated(summary)
	l.Notifier.NotifyDashboard("sandbox_updated")
}

func (l Lifecycle) publishEventAdded(sessionID string, event domain.SandboxEvent) {
	if l.Notifier != nil {
		l.Notifier.PublishEventAdded(sessionID, event)
	}
}

func JupyterTargetReachable(proxyState domain.ProxyState, timeout time.Duration) bool {
	_, port := driverpkg.JupyterConnectTarget(execution.ToDriverProxyState(proxyState))
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", driverpkg.JupyterConnectAddress(execution.ToDriverProxyState(proxyState)), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

package schedulers

import (
	"agent-compose/pkg/events/webhooks"
	domain "agent-compose/pkg/model"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ControllerStore interface {
	RunStore
	SchedulerStore
	EventDeliveryStore

	ListLoaders(ctx context.Context) ([]domain.Scheduler, error)
	GetLoader(ctx context.Context, loaderID string) (domain.Scheduler, error)
	ReplaceLoaderTriggers(ctx context.Context, loaderID string, triggers []domain.SchedulerTrigger) ([]domain.SchedulerTrigger, error)
	SetLoaderEnabled(ctx context.Context, loaderID string, enabled bool) error
	SetLoaderTriggerEnabled(ctx context.Context, loaderID, triggerID string, enabled bool) error
	AddLoaderEvent(ctx context.Context, event domain.SchedulerEvent) error
}

type ControllerNotifier interface {
	Notify(reason string)
}

type ControllerPublisher interface {
	Publish(event domain.SchedulerTopicEvent) bool
}

type ControllerArtifacts interface {
	RunDir(loaderID, runID string) string
	Write(dir, name, content string) error
}

type FSArtifacts struct {
	DataRoot string
}

func (a FSArtifacts) RunDir(loaderID, runID string) string {
	parts := []string{a.DataRoot, "schedulers", strings.TrimSpace(loaderID), "runs"}
	if strings.TrimSpace(runID) != "" {
		parts = append(parts, strings.TrimSpace(runID))
	}
	return filepath.Join(parts...)
}

func (a FSArtifacts) Write(dir, name, content string) error {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(content) == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(strings.TrimSpace(content)+"\n"), 0o644)
}

type ControllerDependencies struct {
	RootCtx      context.Context
	Store        ControllerStore
	Engine       SchedulerEngine
	HostFactory  RunHostFactory
	Notifier     ControllerNotifier
	Publisher    ControllerPublisher
	Artifacts    ControllerArtifacts
	Wake         chan struct{}
	RunTimeout   func(time.Duration) time.Duration
	ReserveSlots func(event domain.SchedulerTopicEvent, count int) ([]*webhooks.Reservation, bool)
	Schedulers   map[string]domain.Scheduler
	Running      map[string]int
	Now          func() time.Time
	NewID        func() string
}

type Controller struct {
	deps ControllerDependencies

	startOnce       sync.Once
	mu              sync.RWMutex
	loaders         map[string]domain.Scheduler
	running         map[string]int
	runExecutor     *RunExecutor
	invocations     *InvocationExecutor
	schedulerRuns   *schedulerRunSupervisor
	scheduler       *Scheduler
	eventDispatcher *EventDispatcher
}

func NewController(deps ControllerDependencies) *Controller {
	if deps.RootCtx == nil {
		deps.RootCtx = context.Background()
	}
	if deps.Wake == nil {
		deps.Wake = make(chan struct{}, 1)
	}
	if deps.Schedulers == nil {
		deps.Schedulers = map[string]domain.Scheduler{}
	}
	if deps.Running == nil {
		deps.Running = map[string]int{}
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	c := &Controller{
		deps:    deps,
		loaders: deps.Schedulers,
		running: deps.Running,
	}
	c.init()
	return c
}

func (c *Controller) init() {
	if c.runExecutor == nil {
		c.runExecutor = NewRunExecutor(RunExecutorDependencies{
			Store:         c.deps.Store,
			Engine:        c.deps.Engine,
			HostFactory:   c.deps.HostFactory,
			ArtifactsDir:  c.RunArtifactsDir,
			WriteArtifact: c.WriteRunArtifact,
			EnterRun:      c.EnterRun,
			LeaveRun:      c.LeaveRun,
			AddLoaderEvent: func(ctx context.Context, loaderID, runID, triggerID, eventType, level, message string, payload any, linkedSandboxID, linkedCellID, linkedAgentThreadID string) error {
				return c.AddLoaderEvent(ctx, loaderID, runID, triggerID, eventType, level, message, payload, linkedSandboxID, linkedCellID, linkedAgentThreadID)
			},
			UpdateTriggerEventDelivery: c.UpdateTriggerEventDelivery,
			Notify:                     c.notify,
			Refresh:                    c.Refresh,
		})
	}
	if c.invocations == nil {
		c.invocations = NewInvocationExecutor(InvocationExecutorDependencies{
			Engine:      c.deps.Engine,
			HostFactory: c.deps.HostFactory,
			EnterRun:    c.EnterRun,
			LeaveRun:    c.LeaveRun,
			NewID:       c.deps.NewID,
		})
	}
	if c.schedulerRuns == nil {
		runStore, _ := c.deps.Store.(schedulerRunStore)
		c.schedulerRuns = newSchedulerRunSupervisor(schedulerRunSupervisorDependencies{
			RootCtx:          c.deps.RootCtx,
			Store:            runStore,
			LoadLoaderForRun: c.LoadLoaderForRun,
			Prepare:          c.Prepare,
			Execute:          c.Execute,
			RunTimeout:       c.runTimeout,
		})
	}
	if c.scheduler == nil {
		c.scheduler = NewScheduler(SchedulerDependencies{
			RootCtx:       c.deps.RootCtx,
			Wake:          c.deps.Wake,
			Store:         c.deps.Store,
			Snapshot:      c.CachedLoadersMap,
			ReplaceCached: c.ReplaceCachedLoaders,
			Run: func(ctx context.Context, loader domain.Scheduler, trigger *domain.SchedulerTrigger, payloadJSON, source string, options RunOptions, triggerEventAck ...func(context.Context) error) (domain.SchedulerRunSummary, error) {
				return c.Run(ctx, loader, trigger, payloadJSON, source, options, triggerEventAck...)
			},
			RunTimeout: c.runTimeout,
		})
	}
	if c.eventDispatcher == nil {
		c.eventDispatcher = NewEventDispatcher(EventDispatcherDependencies{
			RootCtx:      c.deps.RootCtx,
			Store:        c.deps.Store,
			Targets:      func(topic string) []EventTarget { return CollectEventTargets(c.SnapshotLoaders(), topic) },
			IsBusy:       c.AnyTargetBusy,
			ReserveSlots: c.deps.ReserveSlots,
			Run: func(ctx context.Context, loader domain.Scheduler, trigger *domain.SchedulerTrigger, payloadJSON, source string, options RunOptions, triggerEventAck ...func(context.Context) error) (domain.SchedulerRunSummary, error) {
				return c.Run(ctx, loader, trigger, payloadJSON, source, options, triggerEventAck...)
			},
			Prepare:    c.Prepare,
			Execute:    c.Execute,
			Abort:      c.Abort,
			RunTimeout: c.runTimeout,
			EnterRun:   c.EnterRun,
			LeaveRun:   c.LeaveRun,
		})
	}
}

func (c *Controller) Start() {
	if c == nil {
		return
	}
	c.startOnce.Do(func() {
		if err := c.Refresh(c.deps.RootCtx); err != nil {
			slog.Warn("failed to refresh loaders on startup", "error", err)
		}
		go c.scheduler.Loop()
		go c.EventLoop()
	})
}

func (c *Controller) ScheduleLoop() {
	c.scheduler.Loop()
}

func (c *Controller) Refresh(ctx context.Context) error {
	items, err := c.deps.Store.ListLoaders(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]domain.Scheduler, len(items))
	for _, item := range items {
		next[item.Summary.ID] = CloneLoader(item)
	}
	c.mu.Lock()
	clear(c.loaders)
	for id, item := range next {
		c.loaders[id] = item
	}
	c.mu.Unlock()
	c.WakeScheduler()
	return nil
}

func (c *Controller) Validate(ctx context.Context, runtime, script string) (SchedulerValidationResult, error) {
	return c.deps.Engine.Validate(ctx, runtime, script)
}

func (c *Controller) SetLoaderEnabled(ctx context.Context, loaderID string, enabled bool) (domain.Scheduler, error) {
	if err := c.deps.Store.SetLoaderEnabled(ctx, loaderID, enabled); err != nil {
		return domain.Scheduler{}, err
	}
	if err := c.Refresh(ctx); err != nil {
		return domain.Scheduler{}, err
	}
	c.notify("loader_updated")
	return c.deps.Store.GetLoader(ctx, loaderID)
}

func (c *Controller) SetLoaderTriggerEnabled(ctx context.Context, loaderID, triggerID string, enabled bool) (domain.Scheduler, error) {
	if err := c.deps.Store.SetLoaderTriggerEnabled(ctx, loaderID, triggerID, enabled); err != nil {
		return domain.Scheduler{}, err
	}
	if err := c.Refresh(ctx); err != nil {
		return domain.Scheduler{}, err
	}
	c.notify("loader_updated")
	return c.deps.Store.GetLoader(ctx, loaderID)
}

func (c *Controller) RunNow(ctx context.Context, loaderID, triggerID, payloadJSON string, timeout time.Duration) (domain.SchedulerRunSummary, error) {
	loader, trigger, err := c.LoadLoaderForRun(ctx, loaderID, triggerID)
	if err != nil {
		return domain.SchedulerRunSummary{}, err
	}
	runCtx, cancel := context.WithTimeout(c.deps.RootCtx, c.runTimeout(timeout))
	defer cancel()
	return c.Run(runCtx, loader, trigger, payloadJSON, "manual", RunOptions{})
}

func (c *Controller) Run(ctx context.Context, loader domain.Scheduler, trigger *domain.SchedulerTrigger, payloadJSON, source string, options RunOptions, triggerEventAck ...func(context.Context) error) (domain.SchedulerRunSummary, error) {
	return c.runExecutor.Run(ctx, loader, trigger, payloadJSON, source, options, triggerEventAck...)
}

func (c *Controller) Prepare(ctx context.Context, loader domain.Scheduler, trigger *domain.SchedulerTrigger, payloadJSON, source string, options RunOptions) (PreparedRun, error) {
	return c.runExecutor.Prepare(ctx, loader, trigger, payloadJSON, source, options)
}

func (c *Controller) Execute(ctx context.Context, prepared PreparedRun) (domain.SchedulerRunSummary, error) {
	return c.runExecutor.Execute(ctx, prepared)
}

func (c *Controller) Abort(ctx context.Context, prepared PreparedRun, reason string) {
	c.runExecutor.Abort(ctx, prepared, reason)
}

func (c *Controller) Publish(topic string, payload map[string]any) {
	if c.deps.Publisher == nil {
		return
	}
	_ = c.deps.Publisher.Publish(domain.SchedulerTopicEvent{
		Topic:     strings.TrimSpace(topic),
		Payload:   payload,
		CreatedAt: c.now(),
	})
}

func (c *Controller) EventLoop() {
	bus, ok := c.deps.Publisher.(interface {
		Events() <-chan domain.SchedulerTopicEvent
	})
	if !ok || bus == nil {
		return
	}
	for {
		select {
		case <-c.deps.RootCtx.Done():
			return
		case event, ok := <-bus.Events():
			if !ok {
				return
			}
			c.DispatchEvent(event)
		}
	}
}

func (c *Controller) DispatchEvent(event domain.SchedulerTopicEvent) {
	c.eventDispatcher.Dispatch(event)
}

func (c *Controller) CollectDueScheduledRuns(now time.Time) []ScheduledRun {
	return c.scheduler.CollectDue(now)
}

func (c *Controller) DispatchScheduledRuns(jobs []ScheduledRun) {
	c.scheduler.Dispatch(jobs)
}

func (c *Controller) NextScheduledFireAt() (time.Time, bool) {
	return c.scheduler.NextFireAt()
}

func (c *Controller) WakeScheduler() {
	if c == nil || c.deps.Wake == nil {
		return
	}
	select {
	case c.deps.Wake <- struct{}{}:
	default:
	}
}

func (c *Controller) CachedLoadersMap() map[string]domain.Scheduler {
	c.mu.RLock()
	defer c.mu.RUnlock()
	items := make(map[string]domain.Scheduler, len(c.loaders))
	for id, item := range c.loaders {
		items[id] = CloneLoader(item)
	}
	return items
}

func (c *Controller) ReplaceCachedLoaders(updatedLoaders map[string]domain.Scheduler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, item := range updatedLoaders {
		c.loaders[id] = CloneLoader(item)
	}
}

func (c *Controller) SnapshotLoaders() []domain.Scheduler {
	c.mu.RLock()
	defer c.mu.RUnlock()
	items := make([]domain.Scheduler, 0, len(c.loaders))
	for _, item := range c.loaders {
		items = append(items, CloneLoader(item))
	}
	return items
}

func (c *Controller) LoadLoaderForRun(ctx context.Context, loaderID, triggerID string) (domain.Scheduler, *domain.SchedulerTrigger, error) {
	loader, err := c.deps.Store.GetLoader(ctx, loaderID)
	if err != nil {
		return domain.Scheduler{}, nil, err
	}
	if strings.TrimSpace(triggerID) == "" {
		return loader, nil, nil
	}
	triggerID = strings.TrimSpace(triggerID)
	for _, item := range loader.Triggers {
		if item.ID == triggerID {
			current := item
			return loader, &current, nil
		}
	}
	id := strings.TrimSpace(loaderID) + "/" + triggerID
	return domain.Scheduler{}, nil, domain.ResourceError(domain.ErrNotFound, "loader trigger", id, fmt.Sprintf("loader trigger %s not found", id), nil)
}

func (c *Controller) UpdateTriggerEventDelivery(ctx context.Context, run domain.SchedulerRunSummary) {
	if c == nil || c.deps.Store == nil {
		return
	}
	metadata := ParseTriggerEventMetadata(run.PayloadJSON)
	if metadata.EventID == "" || run.SchedulerID == "" || run.TriggerID == "" {
		return
	}
	status := domain.EventDeliveryStatusRunStarted
	errText := ""
	switch run.Status {
	case domain.SchedulerRunStatusSucceeded:
		status = domain.EventDeliveryStatusRunSucceeded
	case domain.SchedulerRunStatusFailed:
		status = domain.EventDeliveryStatusRunFailed
		errText = run.Error
	case domain.SchedulerRunStatusCanceled:
		status = domain.EventDeliveryStatusRunFailed
		errText = run.Error
	case domain.SchedulerRunStatusSkipped:
		status = domain.EventDeliveryStatusSkipped
		errText = run.Error
	}
	if err := c.deps.Store.UpsertEventDelivery(ctx, domain.EventDelivery{
		EventID:     metadata.EventID,
		SchedulerID: run.SchedulerID,
		TriggerID:   run.TriggerID,
		RunID:       run.ID,
		Status:      status,
		Error:       errText,
	}); err != nil {
		slog.Warn("failed to update event delivery", "event_id", metadata.EventID, "loader_id", run.SchedulerID, "trigger_id", run.TriggerID, "run_id", run.ID, "error", err)
	}
}

func (c *Controller) EnterRun(loader domain.Scheduler) bool {
	loaderID := strings.TrimSpace(loader.Summary.ID)
	policy := domain.NormalizeLoaderConcurrencyPolicy(loader.Summary.ConcurrencyPolicy)
	c.mu.Lock()
	defer c.mu.Unlock()
	if policy != domain.SchedulerConcurrencyPolicyParallel && c.running[loaderID] > 0 {
		return false
	}
	c.running[loaderID]++
	return true
}

func (c *Controller) LeaveRun(loaderID string) {
	loaderID = strings.TrimSpace(loaderID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running[loaderID] <= 1 {
		delete(c.running, loaderID)
		return
	}
	c.running[loaderID]--
}

func (c *Controller) AnyTargetBusy(targets []EventTarget) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return AnyTargetBusy(targets, c.running)
}

func (c *Controller) AddLoaderEvent(ctx context.Context, loaderID, runID, triggerID, eventType, level, message string, payload any, linkedSandboxID, linkedCellID, linkedAgentThreadID string) error {
	_, err := c.AddLoaderEventRecord(ctx, loaderID, runID, triggerID, eventType, level, message, payload, linkedSandboxID, linkedCellID, linkedAgentThreadID)
	return err
}

func (c *Controller) AddLoaderEventRecord(ctx context.Context, loaderID, runID, triggerID, eventType, level, message string, payload any, linkedSandboxID, linkedCellID, linkedAgentThreadID string) (domain.SchedulerEvent, error) {
	payloadJSON, err := domain.MarshalJSONCompact(payload)
	if err != nil {
		return domain.SchedulerEvent{}, err
	}
	event := domain.SchedulerEvent{
		ID:                  c.newID(),
		SchedulerID:         strings.TrimSpace(loaderID),
		RunID:               strings.TrimSpace(runID),
		TriggerID:           strings.TrimSpace(triggerID),
		Type:                strings.TrimSpace(eventType),
		Level:               firstNonEmpty(strings.TrimSpace(level), "info"),
		Message:             strings.TrimSpace(message),
		PayloadJSON:         payloadJSON,
		LinkedSandboxID:     strings.TrimSpace(linkedSandboxID),
		LinkedCellID:        strings.TrimSpace(linkedCellID),
		LinkedAgentThreadID: strings.TrimSpace(linkedAgentThreadID),
		CreatedAt:           c.now(),
	}
	if err := c.deps.Store.AddLoaderEvent(ctx, event); err != nil {
		return domain.SchedulerEvent{}, err
	}
	return event, nil
}

func (c *Controller) RunArtifactsDir(loaderID, runID string) string {
	if c.deps.Artifacts == nil {
		return ""
	}
	return c.deps.Artifacts.RunDir(loaderID, runID)
}

func (c *Controller) WriteRunArtifact(dir, name, content string) error {
	if c.deps.Artifacts == nil {
		return nil
	}
	return c.deps.Artifacts.Write(dir, name, content)
}

func (c *Controller) notify(reason string) {
	if c.deps.Notifier != nil {
		c.deps.Notifier.Notify(reason)
	}
}

func (c *Controller) runTimeout(override time.Duration) time.Duration {
	if c.deps.RunTimeout != nil {
		return c.deps.RunTimeout(override)
	}
	if override > 0 {
		return override
	}
	return 20 * time.Minute
}

func (c *Controller) now() time.Time {
	if c.deps.Now == nil {
		return time.Now().UTC()
	}
	return c.deps.Now().UTC()
}

func (c *Controller) newID() string {
	if c.deps.NewID == nil {
		return uuid.NewString()
	}
	return c.deps.NewID()
}

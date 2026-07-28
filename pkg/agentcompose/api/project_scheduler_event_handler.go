package api

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	domain "agent-compose/pkg/model"
	"agent-compose/pkg/schedulers"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

type ProjectSchedulerEventStore interface {
	ListSchedulerEventsPage(context.Context, schedulers.SchedulerEventPageFilter) ([]domain.SchedulerEvent, error)
	CountSchedulerEventsPage(context.Context, schedulers.SchedulerEventPageFilter) (int, error)
}

func (h *ProjectHandler) ListProjectSchedulerEvents(ctx context.Context, req *connect.Request[agentcomposev2.ListProjectSchedulerEventsRequest]) (*connect.Response[agentcomposev2.ListProjectSchedulerEventsResponse], error) {
	_, schedulerRecords, err := h.resolveProjectSchedulerRunTargets(ctx, req.Msg.GetProject(), req.Msg.GetAgentName())
	if err != nil {
		return nil, ConnectErrorForDomain(err)
	}
	agentName := strings.TrimSpace(req.Msg.GetAgentName())
	triggerID := strings.TrimSpace(req.Msg.GetTriggerId())
	runID := strings.TrimSpace(req.Msg.GetRunId())
	if runID != "" {
		_, runScheduler, run, resolveErr := h.resolveProjectSchedulerRun(ctx, req.Msg.GetProject(), runID)
		if resolveErr != nil {
			return nil, ConnectErrorForDomain(resolveErr)
		}
		if agentName != "" && agentName != runScheduler.AgentName {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("scheduler run does not belong to scheduler %q", agentName))
		}
		if triggerID != "" && triggerID != run.TriggerID {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("scheduler run does not belong to trigger %q", triggerID))
		}
		triggerID = run.TriggerID
		runID = run.ID
		schedulerRecords = []domain.ProjectSchedulerRecord{runScheduler}
	}
	offset, limit, err := listPagination(req.Msg.GetOffset(), req.Msg.GetLimit())
	if err != nil {
		return nil, err
	}
	schedulerIDs := make([]string, 0, len(schedulerRecords))
	bySchedulerID := make(map[string]domain.ProjectSchedulerRecord, len(schedulerRecords))
	for _, scheduler := range schedulerRecords {
		schedulerID := strings.TrimSpace(scheduler.ID)
		if schedulerID == "" {
			continue
		}
		schedulerIDs = append(schedulerIDs, schedulerID)
		bySchedulerID[schedulerID] = scheduler
	}
	store, ok := h.store.(ProjectSchedulerEventStore)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scheduler event store is required"))
	}
	events, err := store.ListSchedulerEventsPage(ctx, schedulers.SchedulerEventPageFilter{
		SchedulerIDs:   schedulerIDs,
		RequireTrigger: true,
		TriggerID:      triggerID,
		RunID:          runID,
		Offset:         offset,
		Limit:          limit,
	})
	if err != nil {
		return nil, ConnectErrorForDomain(err)
	}
	total, err := store.CountSchedulerEventsPage(ctx, schedulers.SchedulerEventPageFilter{
		SchedulerIDs: schedulerIDs, RequireTrigger: true, TriggerID: triggerID, RunID: runID,
	})
	if err != nil {
		return nil, ConnectErrorForDomain(err)
	}
	response := &agentcomposev2.ListProjectSchedulerEventsResponse{Events: make([]*agentcomposev2.SchedulerEvent, 0, len(events)), Total: uint32(total)}
	for _, event := range events {
		scheduler, ok := bySchedulerID[event.SchedulerID]
		if !ok {
			continue
		}
		response.Events = append(response.Events, schedulerEventToProto(event, scheduler))
	}
	return connect.NewResponse(response), nil
}

func schedulerEventToProto(event domain.SchedulerEvent, scheduler domain.ProjectSchedulerRecord) *agentcomposev2.SchedulerEvent {
	return &agentcomposev2.SchedulerEvent{
		Id:                  event.ID,
		Type:                event.Type,
		Level:               event.Level,
		Message:             event.Message,
		PayloadJson:         event.PayloadJSON,
		RunId:               event.RunID,
		TriggerId:           event.TriggerID,
		CreatedAt:           projectTimestamp(event.CreatedAt),
		AgentName:           scheduler.AgentName,
		SchedulerId:         scheduler.SchedulerID,
		LinkedSandboxId:     event.LinkedSandboxID,
		LinkedCellId:        event.LinkedCellID,
		LinkedAgentThreadId: event.LinkedAgentThreadID,
	}
}

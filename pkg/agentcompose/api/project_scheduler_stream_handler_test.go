package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"

	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
	"agent-compose/proto/agentcompose/v2/agentcomposev2connect"
)

func TestStreamSchedulerRunsBatchesAndCompletes(t *testing.T) {
	store, _, handler := newSchedulerRunHandlerFixture()
	startedAt := time.Unix(500, 0).UTC()
	store.runs = []domain.LoaderRunSummary{
		{ID: "run-3", LoaderID: store.scheduler.ManagedLoaderID, TriggerID: "trigger-1", Status: domain.LoaderRunStatusSucceeded, StartedAt: startedAt.Add(2 * time.Second)},
		{ID: "run-2", LoaderID: store.scheduler.ManagedLoaderID, TriggerID: "trigger-1", Status: domain.LoaderRunStatusSucceeded, StartedAt: startedAt.Add(time.Second)},
		{ID: "run-1", LoaderID: store.scheduler.ManagedLoaderID, TriggerID: "trigger-1", Status: domain.LoaderRunStatusSucceeded, StartedAt: startedAt},
	}
	client, closeServer := schedulerStreamTestClient(t, handler)
	defer closeServer()
	stream, err := client.StreamSchedulerRuns(context.Background(), connect.NewRequest(&agentcomposev2.StreamSchedulerRunsRequest{
		Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}}, BatchSize: 1, Limit: 2,
	}))
	if err != nil {
		t.Fatalf("StreamSchedulerRuns returned error: %v", err)
	}
	var frames []*agentcomposev2.StreamSchedulerRunsResponse
	for stream.Receive() {
		frames = append(frames, stream.Msg())
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("StreamSchedulerRuns receive error: %v", err)
	}
	if len(frames) != 3 || frames[0].GetRuns()[0].GetRunId() != "run-3" || frames[1].GetRuns()[0].GetRunId() != "run-2" ||
		!frames[2].GetComplete() || !frames[2].GetTruncated() || frames[2].GetEmittedCount() != 2 || frames[2].GetCheckpoint() == "" {
		t.Fatalf("stream frames = %#v", frames)
	}
	assertSchedulerStreamHeaders(t, stream.ResponseHeader())
}

func TestStreamProjectSchedulerEventsTailsInDisplayOrder(t *testing.T) {
	store, _, handler := newSchedulerRunHandlerFixture()
	createdAt := time.Unix(600, 0).UTC()
	store.events = []domain.LoaderEvent{
		{ID: "event-3", LoaderID: store.scheduler.ManagedLoaderID, RunID: "run-1", TriggerID: "trigger-1", Type: "loader.log", CreatedAt: createdAt.Add(2 * time.Second)},
		{ID: "event-2", LoaderID: store.scheduler.ManagedLoaderID, RunID: "run-1", TriggerID: "trigger-1", Type: "loader.log", CreatedAt: createdAt.Add(time.Second)},
		{ID: "event-1", LoaderID: store.scheduler.ManagedLoaderID, RunID: "run-1", TriggerID: "trigger-1", Type: "loader.log", CreatedAt: createdAt},
	}
	client, closeServer := schedulerStreamTestClient(t, handler)
	defer closeServer()
	stream, err := client.StreamProjectSchedulerEvents(context.Background(), connect.NewRequest(&agentcomposev2.StreamProjectSchedulerEventsRequest{
		Project: &agentcomposev2.ProjectRef{Selector: &agentcomposev2.ProjectRef_ProjectId{ProjectId: store.project.ID}}, BatchSize: 1, Tail: 2,
	}))
	if err != nil {
		t.Fatalf("StreamProjectSchedulerEvents returned error: %v", err)
	}
	var eventIDs []string
	var completed *agentcomposev2.StreamProjectSchedulerEventsResponse
	for stream.Receive() {
		frame := stream.Msg()
		for _, event := range frame.GetEvents() {
			eventIDs = append(eventIDs, event.GetId())
		}
		if frame.GetComplete() {
			completed = frame
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("StreamProjectSchedulerEvents receive error: %v", err)
	}
	if len(eventIDs) != 2 || eventIDs[0] != "event-2" || eventIDs[1] != "event-3" || completed == nil || completed.GetEmittedCount() != 2 {
		t.Fatalf("event ids/completion = %#v / %#v", eventIDs, completed)
	}
	assertSchedulerStreamHeaders(t, stream.ResponseHeader())
}

func schedulerStreamTestClient(t *testing.T, handler *ProjectHandler) (agentcomposev2connect.ProjectServiceClient, func()) {
	t.Helper()
	mux := http.NewServeMux()
	path, connectHandler := agentcomposev2connect.NewProjectServiceHandler(handler)
	mux.Handle(path, connectHandler)
	server := httptest.NewServer(mux)
	return agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL), server.Close
}

func assertSchedulerStreamHeaders(t *testing.T, headers http.Header) {
	t.Helper()
	if headers.Get("Cache-Control") != "no-cache, no-transform" || headers.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("stream headers = %#v", headers)
	}
}

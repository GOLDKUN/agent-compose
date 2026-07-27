package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	agentcomposev2 "agent-compose/proto/agentcompose/v2"
	"agent-compose/proto/agentcompose/v2/agentcomposev2connect"
)

func TestIntegrationListSandboxesRejectsInvalidStatusOverConnect(t *testing.T) {
	store := &characterizationSandboxStore{}
	handler := NewSandboxHandler(&characterizationSessionDelegate{}, store, &characterizationSandboxRemover{}, nil)
	path, serviceHandler := agentcomposev2connect.NewSandboxServiceHandler(handler)
	mux := http.NewServeMux()
	mux.Handle(path, serviceHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := agentcomposev2connect.NewSandboxServiceClient(server.Client(), server.URL)

	_, err := client.ListSandboxes(context.Background(), connect.NewRequest(&agentcomposev2.ListSandboxesRequest{
		Status: []string{"running", "definitely-invalid"},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), `invalid sandbox status "definitely-invalid"`) {
		t.Fatalf("ListSandboxes() code/error = %v / %v", connect.CodeOf(err), err)
	}
	if len(store.listOptions) != 0 {
		t.Fatalf("invalid status reached store with options %#v", store.listOptions)
	}
}

package agentcomposev2

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestExecutionRPCNamesAndStreamingShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		service         protoreflect.Name
		method          protoreflect.Name
		input           protoreflect.FullName
		output          protoreflect.FullName
		clientStreaming bool
		serverStreaming bool
	}{
		{"RunService", "RunAgent", "agentcompose.v2.RunAgentRequest", "agentcompose.v2.RunAgentResponse", false, false},
		{"RunService", "StartAgentRun", "agentcompose.v2.StartAgentRunRequest", "agentcompose.v2.StartAgentRunResponse", false, false},
		{"RunService", "StreamAgentRun", "agentcompose.v2.RunAgentRequest", "agentcompose.v2.StreamAgentRunResponse", false, true},
		{"RunService", "AttachAgentRun", "agentcompose.v2.AttachAgentRunRequest", "agentcompose.v2.AttachAgentRunResponse", true, true},
		{"ExecService", "Exec", "agentcompose.v2.ExecRequest", "agentcompose.v2.ExecResponse", false, false},
		{"ExecService", "StreamExec", "agentcompose.v2.ExecRequest", "agentcompose.v2.StreamExecResponse", false, true},
		{"ExecService", "AttachExec", "agentcompose.v2.AttachExecRequest", "agentcompose.v2.AttachExecResponse", true, true},
		{"ProjectService", "InvokeScheduler", "agentcompose.v2.InvokeSchedulerRequest", "agentcompose.v2.InvokeSchedulerResponse", false, false},
		{"ProjectService", "RunScheduler", "agentcompose.v2.RunSchedulerRequest", "agentcompose.v2.RunSchedulerResponse", false, false},
		{"ProjectService", "StartSchedulerRun", "agentcompose.v2.StartSchedulerRunRequest", "agentcompose.v2.StartSchedulerRunResponse", false, false},
	}

	services := File_agentcompose_v2_agentcompose_proto.Services()
	for _, test := range tests {
		t.Run(string(test.service)+"/"+string(test.method), func(t *testing.T) {
			service := services.ByName(test.service)
			if service == nil {
				t.Fatalf("service %q not found", test.service)
			}
			method := service.Methods().ByName(test.method)
			if method == nil {
				t.Fatalf("method %q not found", test.method)
			}
			if got := method.Input().FullName(); got != test.input {
				t.Errorf("input = %q, want %q", got, test.input)
			}
			if got := method.Output().FullName(); got != test.output {
				t.Errorf("output = %q, want %q", got, test.output)
			}
			if got := method.IsStreamingClient(); got != test.clientStreaming {
				t.Errorf("client streaming = %t, want %t", got, test.clientStreaming)
			}
			if got := method.IsStreamingServer(); got != test.serverStreaming {
				t.Errorf("server streaming = %t, want %t", got, test.serverStreaming)
			}
		})
	}
}

func TestLegacyExecutionRPCNamesAreNotExposed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		service protoreflect.Name
		methods []protoreflect.Name
	}{
		{"RunService", []protoreflect.Name{"StartRun", "RunAgentStream", "RunAttach"}},
		{"ExecService", []protoreflect.Name{"ExecStream", "ExecAttach"}},
	}

	services := File_agentcompose_v2_agentcompose_proto.Services()
	for _, test := range tests {
		service := services.ByName(test.service)
		if service == nil {
			t.Fatalf("service %q not found", test.service)
		}
		for _, method := range test.methods {
			if service.Methods().ByName(method) != nil {
				t.Errorf("legacy method %s/%s is still exposed", test.service, method)
			}
		}
	}
}

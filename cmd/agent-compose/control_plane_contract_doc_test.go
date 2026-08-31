package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	agentcomposev2 "github.com/chaitin/agent-compose/proto/agentcompose/v2"
)

var (
	documentedRPCPattern  = regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9]*Service)\.([A-Za-z][A-Za-z0-9]*)\b`)
	documentedCodePattern = regexp.MustCompile("`([^`]+)`")
	documentedNamePattern = regexp.MustCompile(`\b[A-Z][A-Za-z0-9]+\b`)
)

func TestControlPlaneRPCMatrixReferencesCurrentDescriptor(t *testing.T) {
	t.Parallel()

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	documentPath := filepath.Join(filepath.Dir(sourceFile), "..", "..", "docs", "design", "control_plane_transport_contract.md")
	contents, err := os.ReadFile(documentPath)
	if err != nil {
		t.Fatalf("read control-plane contract: %v", err)
	}

	matrixStart := regexp.MustCompile(`(?m)^## CLI command to RPC matrix$`).FindIndex(contents)
	matrixEnd := regexp.MustCompile(`(?m)^## Approved non-Connect HTTP routes$`).FindIndex(contents)
	if matrixStart == nil || matrixEnd == nil || matrixStart[0] >= matrixEnd[0] {
		t.Fatal("control-plane RPC matrices not found")
	}
	matrix := contents[matrixStart[0]:matrixEnd[0]]
	matches := documentedRPCPattern.FindAllSubmatch(matrix, -1)
	if len(matches) == 0 {
		t.Fatal("control-plane contract contains no Service.Method references")
	}

	services := agentcomposev2.File_agentcompose_v2_agentcompose_proto.Services()
	serviceNames := make(map[string]struct{}, services.Len())
	methodNames := make(map[string]struct{})
	for serviceIndex := 0; serviceIndex < services.Len(); serviceIndex++ {
		service := services.Get(serviceIndex)
		serviceNames[string(service.Name())] = struct{}{}
		for methodIndex := 0; methodIndex < service.Methods().Len(); methodIndex++ {
			methodNames[string(service.Methods().Get(methodIndex).Name())] = struct{}{}
		}
	}

	for _, match := range matches {
		serviceName := string(match[1])
		methodName := string(match[2])
		service := services.ByName(protoreflect.Name(serviceName))
		if service == nil {
			t.Errorf("documented service %s does not exist", serviceName)
			continue
		}
		if service.Methods().ByName(protoreflect.Name(methodName)) == nil {
			t.Errorf("documented RPC %s.%s does not exist", serviceName, methodName)
		}
	}

	for _, codeMatch := range documentedCodePattern.FindAllSubmatch(matrix, -1) {
		for _, name := range documentedNamePattern.FindAllString(string(codeMatch[1]), -1) {
			if _, ok := serviceNames[name]; ok {
				continue
			}
			if _, ok := methodNames[name]; ok {
				continue
			}
			t.Errorf("documented RPC or service name %s does not exist", name)
		}
	}
}

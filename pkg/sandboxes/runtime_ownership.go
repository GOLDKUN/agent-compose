package sandboxes

import (
	"fmt"
	"os"
	"strings"

	domain "agent-compose/pkg/model"
)

func MarkSandboxRuntimeReleased(sandboxRoot string, sandbox *domain.Sandbox) error {
	if sandbox == nil {
		return fmt.Errorf("sandbox is required")
	}
	record, err := ReadOwnershipRecord(sandboxRoot, sandbox.Summary.ID)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		record = ownershipFromSandbox(sandboxRoot, sandbox)
	}
	record.RuntimeID = ""
	resources := record.OwnedResources[:0]
	for _, resource := range record.OwnedResources {
		if resource.Kind != "runtime" {
			resources = append(resources, resource)
		}
	}
	record.OwnedResources = resources
	return WriteOwnershipRecord(sandboxRoot, record)
}

func MarkSandboxRuntimeOwned(sandboxRoot string, sandbox *domain.Sandbox) error {
	if sandbox == nil {
		return fmt.Errorf("sandbox is required")
	}
	record, err := ReadOwnershipRecord(sandboxRoot, sandbox.Summary.ID)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		record = ownershipFromSandbox(sandboxRoot, sandbox)
	}
	runtimeID := strings.TrimSpace(sandbox.Summary.RuntimeRef)
	record.RuntimeID = runtimeID
	found := false
	for index := range record.OwnedResources {
		if record.OwnedResources[index].Kind != "runtime" {
			continue
		}
		record.OwnedResources[index].Identity = runtimeID
		found = true
	}
	if !found && runtimeID != "" {
		record.OwnedResources = append(record.OwnedResources, OwnedResource{Kind: "runtime", Identity: runtimeID})
	}
	return WriteOwnershipRecord(sandboxRoot, record)
}

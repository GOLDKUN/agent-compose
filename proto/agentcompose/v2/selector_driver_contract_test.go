package agentcomposev2

import "testing"

func TestExecSandboxProjectSelectorsShareOneof(t *testing.T) {
	message := (&ExecSandboxSelector{}).ProtoReflect().Descriptor()
	project := message.Oneofs().ByName("project")
	if project == nil {
		t.Fatal("ExecSandboxSelector.project oneof is missing")
	}
	for _, fieldName := range []string{"project_id", "project_name"} {
		field := message.Fields().ByName(fieldName)
		if field == nil || field.ContainingOneof() != project {
			t.Fatalf("ExecSandboxSelector.%s is not part of project", fieldName)
		}
	}
	if field := message.Fields().ByName("agent_name"); field == nil || field.ContainingOneof() != nil {
		t.Fatal("ExecSandboxSelector.agent_name must remain an independent narrowing filter")
	}
}

func TestDriverSpecUsesSingleConfigOneof(t *testing.T) {
	message := (&DriverSpec{}).ProtoReflect().Descriptor()
	config := message.Oneofs().ByName("config")
	if config == nil {
		t.Fatal("DriverSpec.config oneof is missing")
	}
	for _, fieldName := range []string{"boxlite", "docker", "microsandbox"} {
		field := message.Fields().ByName(fieldName)
		if field == nil || field.ContainingOneof() != config {
			t.Fatalf("DriverSpec.%s is not part of config", fieldName)
		}
	}
	if field := message.Fields().ByName("name"); field == nil || field.ContainingOneof() != nil {
		t.Fatal("DriverSpec.name must remain an independent compatibility field")
	}
}

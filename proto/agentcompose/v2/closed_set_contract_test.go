package agentcomposev2

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestSchedulerRunAndTriggerSpecShareTriggerKindEnum(t *testing.T) {
	t.Parallel()

	messages := File_agentcompose_v2_agentcompose_proto.Messages()
	want := File_agentcompose_v2_agentcompose_proto.Enums().ByName("TriggerKind")
	if want == nil {
		t.Fatal("TriggerKind enum not found")
	}

	for _, messageName := range []string{"SchedulerRun", "TriggerSpec"} {
		message := messages.ByName(protoreflect.Name(messageName))
		if message == nil {
			t.Fatalf("message %s not found", messageName)
		}
		field := message.Fields().ByName("trigger_kind")
		if messageName == "TriggerSpec" {
			field = message.Fields().ByName("kind")
		}
		if field == nil {
			t.Fatalf("trigger kind field not found on %s", messageName)
		}
		if field.Enum() == nil || field.Enum().FullName() != want.FullName() {
			t.Fatalf("%s trigger kind enum = %v, want %s", messageName, field.Enum(), want.FullName())
		}
	}
}

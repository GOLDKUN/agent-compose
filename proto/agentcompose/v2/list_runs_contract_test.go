package agentcomposev2

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestListRunsTimeFiltersUseTimestampFields(t *testing.T) {
	descriptor := (&ListRunsRequest{}).ProtoReflect().Descriptor()
	for _, name := range []protoreflect.Name{"started_from", "started_to"} {
		field := descriptor.Fields().ByName(name)
		if field == nil {
			t.Fatalf("field %s is missing", name)
		}
		if field.Kind() != protoreflect.MessageKind || field.Message().FullName() != "google.protobuf.Timestamp" {
			t.Fatalf("field %s type = %s, want google.protobuf.Timestamp", name, field.Kind())
		}
	}
}

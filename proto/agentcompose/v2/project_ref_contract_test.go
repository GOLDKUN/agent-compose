package agentcomposev2

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestProjectRefSelectorsShareOneof(t *testing.T) {
	t.Parallel()
	descriptor := (&ProjectRef{}).ProtoReflect().Descriptor()
	oneofs := descriptor.Oneofs()
	if oneofs.Len() != 1 || string(oneofs.Get(0).Name()) != "selector" {
		t.Fatalf("ProjectRef oneofs = %v, want selector", oneofs.Len())
	}
	selector := oneofs.Get(0)
	for _, fieldName := range []string{"project_id", "name", "source_path"} {
		field := descriptor.Fields().ByName(protoreflect.Name(fieldName))
		if field == nil || field.ContainingOneof() != selector {
			t.Fatalf("ProjectRef.%s is not part of selector", fieldName)
		}
	}
}

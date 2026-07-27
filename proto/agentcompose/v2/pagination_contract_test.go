package agentcomposev2

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestListRPCsUseUniformPaginationFields(t *testing.T) {
	for serviceIndex := 0; serviceIndex < File_agentcompose_v2_agentcompose_proto.Services().Len(); serviceIndex++ {
		service := File_agentcompose_v2_agentcompose_proto.Services().Get(serviceIndex)
		for methodIndex := 0; methodIndex < service.Methods().Len(); methodIndex++ {
			method := service.Methods().Get(methodIndex)
			if !strings.HasPrefix(string(method.Name()), "List") {
				continue
			}
			assertUint32Field(t, method, method.Input(), "offset")
			assertUint32Field(t, method, method.Input(), "limit")
			assertUint32Field(t, method, method.Output(), "total")
			for _, forbidden := range []protoreflect.Name{"cursor", "next_cursor", "next_offset", "has_more", "total_count"} {
				if field := method.Input().Fields().ByName(forbidden); field != nil {
					t.Errorf("%s request still exposes %s", method.FullName(), forbidden)
				}
				if field := method.Output().Fields().ByName(forbidden); field != nil {
					t.Errorf("%s response still exposes %s", method.FullName(), forbidden)
				}
			}
		}
	}
}

func assertUint32Field(t *testing.T, method protoreflect.MethodDescriptor, message protoreflect.MessageDescriptor, name protoreflect.Name) {
	t.Helper()
	field := message.Fields().ByName(name)
	if field == nil {
		t.Errorf("%s message %s is missing %s", method.FullName(), message.FullName(), name)
		return
	}
	if field.Kind() != protoreflect.Uint32Kind {
		t.Errorf("%s message %s field %s has kind %s, want uint32", method.FullName(), message.FullName(), name, field.Kind())
	}
}

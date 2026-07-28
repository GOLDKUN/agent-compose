package agentcomposev2

import (
	"fmt"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestV2FieldNumbersHaveNoUnexplainedGaps(t *testing.T) {
	intentionalGaps := map[protoreflect.FullName]map[protoreflect.FieldNumber]struct{}{
		"agentcompose.v2.AttachAgentRunRequest":  fieldNumberRange(8, 14),
		"agentcompose.v2.AttachAgentRunResponse": fieldNumberRange(7, 14),
		"agentcompose.v2.AttachExecRequest":      fieldNumberRange(8, 14),
		"agentcompose.v2.AttachExecResponse":     fieldNumberRange(7, 14),
	}

	assertMessageNumbersHaveNoGaps(t, File_agentcompose_v2_agentcompose_proto.Messages(), intentionalGaps)
	assertEnumNumbersHaveNoGaps(t, File_agentcompose_v2_agentcompose_proto.Enums())
}

func TestV2FreezeFieldLayout(t *testing.T) {
	want := map[protoreflect.FullName]map[protoreflect.Name]protoreflect.FieldNumber{
		"agentcompose.v2.ProjectScheduler":       {"enabled": 4, "description": 7},
		"agentcompose.v2.ProjectSpec":            {"agents": 3, "octobus_servers": 7},
		"agentcompose.v2.RunAgentRequest":        {"env": 5, "payload_json": 16},
		"agentcompose.v2.ListRunsRequest":        {"scheduler_id": 3, "sandbox_id": 10},
		"agentcompose.v2.RunSummary":             {"exit_code": 11, "sandbox_short_id": 21},
		"agentcompose.v2.TranscriptEvent":        {"name": 3, "created_at": 5},
		"agentcompose.v2.ListImagesResponse":     {"store_status": 3},
		"agentcompose.v2.PruneCachesRequest":     {"force": 2},
		"agentcompose.v2.CacheItem":              {"status": 10, "warnings": 16},
		"agentcompose.v2.AttachAgentRunRequest":  {"start": 1, "cancel": 7, "client_frame_id": 15},
		"agentcompose.v2.AttachAgentRunResponse": {"started": 1, "error": 6, "server_frame_id": 15, "created_at": 16},
		"agentcompose.v2.AttachExecRequest":      {"start": 1, "human_message": 7, "client_frame_id": 15},
		"agentcompose.v2.AttachExecResponse":     {"started": 1, "agent_turn_completed": 6, "server_frame_id": 15, "created_at": 16},
	}

	messages := File_agentcompose_v2_agentcompose_proto.Messages()
	for messageName, fields := range want {
		message := messages.ByName(messageName.Name())
		if message == nil {
			t.Fatalf("message %s not found", messageName)
		}
		for fieldName, number := range fields {
			field := message.Fields().ByName(fieldName)
			if field == nil {
				t.Errorf("%s.%s not found", messageName, fieldName)
				continue
			}
			if field.Number() != number {
				t.Errorf("%s.%s number = %d, want %d", messageName, fieldName, field.Number(), number)
			}
		}
	}

	value := File_agentcompose_v2_agentcompose_proto.Enums().ByName("CacheDomain").Values().ByName("CACHE_DOMAIN_SKILL_ARTIFACT_CACHE")
	if value.Number() != 4 {
		t.Errorf("CACHE_DOMAIN_SKILL_ARTIFACT_CACHE number = %d, want 4", value.Number())
	}
}

func assertMessageNumbersHaveNoGaps(t *testing.T, messages protoreflect.MessageDescriptors, intentional map[protoreflect.FullName]map[protoreflect.FieldNumber]struct{}) {
	t.Helper()
	for messageIndex := 0; messageIndex < messages.Len(); messageIndex++ {
		message := messages.Get(messageIndex)
		fields := message.Fields()
		var maximum protoreflect.FieldNumber
		present := make(map[protoreflect.FieldNumber]struct{}, fields.Len())
		for fieldIndex := 0; fieldIndex < fields.Len(); fieldIndex++ {
			number := fields.Get(fieldIndex).Number()
			present[number] = struct{}{}
			if number > maximum {
				maximum = number
			}
		}
		for number := protoreflect.FieldNumber(1); number <= maximum; number++ {
			if _, ok := present[number]; ok {
				continue
			}
			if message.ReservedRanges().Has(number) {
				continue
			}
			if _, ok := intentional[message.FullName()][number]; !ok {
				t.Errorf("%s has unexplained field-number gap at %d", message.FullName(), number)
			}
		}
		assertMessageNumbersHaveNoGaps(t, message.Messages(), intentional)
		assertEnumNumbersHaveNoGaps(t, message.Enums())
	}
}

func assertEnumNumbersHaveNoGaps(t *testing.T, enums protoreflect.EnumDescriptors) {
	t.Helper()
	for enumIndex := 0; enumIndex < enums.Len(); enumIndex++ {
		enum := enums.Get(enumIndex)
		values := enum.Values()
		var maximum protoreflect.EnumNumber
		present := make(map[protoreflect.EnumNumber]struct{}, values.Len())
		for valueIndex := 0; valueIndex < values.Len(); valueIndex++ {
			number := values.Get(valueIndex).Number()
			present[number] = struct{}{}
			if number > maximum {
				maximum = number
			}
		}
		for number := protoreflect.EnumNumber(0); number <= maximum; number++ {
			if _, ok := present[number]; ok {
				continue
			}
			if !enum.ReservedRanges().Has(number) {
				t.Errorf("%s has unexplained enum-number gap at %d", enum.FullName(), number)
			}
		}
	}
}

func fieldNumberRange(first, last protoreflect.FieldNumber) map[protoreflect.FieldNumber]struct{} {
	if first > last {
		panic(fmt.Sprintf("invalid field-number range %d..%d", first, last))
	}
	numbers := make(map[protoreflect.FieldNumber]struct{}, last-first+1)
	for number := first; number <= last; number++ {
		numbers[number] = struct{}{}
	}
	return numbers
}

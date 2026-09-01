package model_test

import (
	"slices"
	"testing"

	"github.com/chaitin/agent-compose/pkg/events"
	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestTopicEventSourceFilterValuesIncludeLegacyLoaderAlias(t *testing.T) {
	for _, source := range []string{domain.TopicEventSourceScheduler, "loader"} {
		got := events.SourceFilterValues(source)
		want := []string{domain.TopicEventSourceScheduler, "loader"}
		if !slices.Equal(got, want) {
			t.Fatalf("TopicEventSourceFilterValues(%q) = %#v, want %#v", source, got, want)
		}
	}

	for _, source := range []string{domain.TopicEventSourceWebhook, domain.TopicEventSourceSystem} {
		got := events.SourceFilterValues(source)
		if !slices.Equal(got, []string{source}) {
			t.Fatalf("TopicEventSourceFilterValues(%q) = %#v, want [%q]", source, got, source)
		}
	}
	if got := events.SourceFilterValues("unknown"); got != nil {
		t.Fatalf("TopicEventSourceFilterValues(unknown) = %#v, want nil", got)
	}
}

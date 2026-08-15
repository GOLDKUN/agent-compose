package events

import "strings"

// TriggerTopicMatches reports whether a scheduler trigger's topic pattern
// selects a published topic. A trailing "*" matches any suffix.
func TriggerTopicMatches(pattern, topic string) bool {
	pattern = strings.TrimSpace(pattern)
	topic = strings.TrimSpace(topic)
	if pattern == "" || topic == "" {
		return false
	}
	if pattern == topic {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(topic, strings.TrimSuffix(pattern, "*"))
	}
	return false
}

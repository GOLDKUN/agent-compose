// Package storeutil holds small, dependency-free helpers shared across the
// storage layer and its consumers, such as decoding timestamps persisted by the
// stores. Keeping these primitives here lets packages parse stored values
// without depending on a concrete store implementation.
package storeutil

import (
	"strconv"
	"strings"
	"time"
)

// StoredUnixMillisecondThreshold is the boundary used to tell stored
// unix-second timestamps apart from unix-millisecond timestamps. Values at or
// above the threshold are treated as milliseconds.
const StoredUnixMillisecondThreshold int64 = 10_000_000_000

// ParseStoredUnixTimeAuto interprets a stored integer timestamp as either unix
// seconds or unix milliseconds based on StoredUnixMillisecondThreshold, and
// returns a UTC time. Non-positive values yield the zero time.
func ParseStoredUnixTimeAuto(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	if value >= StoredUnixMillisecondThreshold {
		return time.UnixMilli(value).UTC()
	}
	return time.Unix(value, 0).UTC()
}

// ParseStoredTime decodes timestamp values returned by SQLite drivers. It
// accepts unix seconds or milliseconds in numeric and textual forms, along
// with the timestamp layouts historically persisted by the stores. Unknown or
// invalid values yield the zero time.
func ParseStoredTime(value any) time.Time {
	switch typed := value.(type) {
	case nil:
		return time.Time{}
	case int64:
		return ParseStoredUnixTimeAuto(typed)
	case int:
		return ParseStoredUnixTimeAuto(int64(typed))
	case float64:
		return ParseStoredUnixTimeAuto(int64(typed))
	case []byte:
		return ParseStoredTime(string(typed))
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return time.Time{}
		}
		if unixValue, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return ParseStoredUnixTimeAuto(unixValue)
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
			if parsed, err := time.Parse(layout, trimmed); err == nil {
				return parsed.UTC()
			}
		}
	}
	return time.Time{}
}

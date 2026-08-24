package storeutil

import (
	"testing"
	"time"
)

func TestParseStoredUnixTimeAuto(t *testing.T) {
	cases := []struct {
		name  string
		value int64
		want  time.Time
	}{
		{name: "zero yields zero time", value: 0, want: time.Time{}},
		{name: "negative yields zero time", value: -1, want: time.Time{}},
		{
			name:  "below threshold treated as seconds",
			value: 1_700_000_000,
			want:  time.Unix(1_700_000_000, 0).UTC(),
		},
		{
			name:  "at threshold treated as milliseconds",
			value: StoredUnixMillisecondThreshold,
			want:  time.UnixMilli(StoredUnixMillisecondThreshold).UTC(),
		},
		{
			name:  "above threshold treated as milliseconds",
			value: 1_700_000_000_000,
			want:  time.UnixMilli(1_700_000_000_000).UTC(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseStoredUnixTimeAuto(tc.value)
			if !got.Equal(tc.want) {
				t.Fatalf("ParseStoredUnixTimeAuto(%d) = %v, want %v", tc.value, got, tc.want)
			}
			if !got.IsZero() && got.Location() != time.UTC {
				t.Errorf("expected UTC location, got %v", got.Location())
			}
		})
	}
}

func TestParseStoredTime(t *testing.T) {
	seconds := time.Unix(1_700_000_000, 0).UTC()
	milliseconds := time.UnixMilli(1_700_000_000_123).UTC()
	rfc3339Nano := time.Date(2026, 7, 1, 2, 3, 4, 123456789, time.UTC)

	cases := []struct {
		name  string
		value any
		want  time.Time
	}{
		{name: "nil", value: nil, want: time.Time{}},
		{name: "integer seconds", value: int(1_700_000_000), want: seconds},
		{name: "int64 milliseconds", value: int64(1_700_000_000_123), want: milliseconds},
		{name: "float64 milliseconds", value: float64(1_700_000_000_123), want: milliseconds},
		{name: "byte slice unix seconds", value: []byte("1700000000"), want: seconds},
		{name: "string unix milliseconds", value: " 1700000000123 ", want: milliseconds},
		{name: "RFC3339", value: "2026-07-01T02:03:04Z", want: time.Date(2026, 7, 1, 2, 3, 4, 0, time.UTC)},
		{name: "RFC3339 nano", value: "2026-07-01T02:03:04.123456789Z", want: rfc3339Nano},
		{name: "legacy milliseconds layout", value: "2026-07-01T02:03:04.000Z", want: time.Date(2026, 7, 1, 2, 3, 4, 0, time.UTC)},
		{name: "empty", value: "  ", want: time.Time{}},
		{name: "invalid string", value: "not-time", want: time.Time{}},
		{name: "unsupported type", value: struct{}{}, want: time.Time{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseStoredTime(tc.value)
			if !got.Equal(tc.want) {
				t.Fatalf("ParseStoredTime(%v) = %v, want %v", tc.value, got, tc.want)
			}
			if !got.IsZero() && got.Location() != time.UTC {
				t.Errorf("expected UTC location, got %v", got.Location())
			}
		})
	}
}

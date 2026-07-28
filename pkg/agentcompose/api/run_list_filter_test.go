package api

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestListRunsStartedRange(t *testing.T) {
	from := time.Date(2026, time.July, 28, 8, 30, 0, 123000000, time.FixedZone("UTC+8", 8*60*60))
	to := from.Add(2 * time.Hour)

	tests := []struct {
		name      string
		from      *timestamppb.Timestamp
		to        *timestamppb.Timestamp
		wantFrom  *time.Time
		wantTo    *time.Time
		wantError bool
	}{
		{name: "unbounded"},
		{name: "from only", from: timestamppb.New(from), wantFrom: &from},
		{name: "to only", to: timestamppb.New(to), wantTo: &to},
		{name: "closed interval", from: timestamppb.New(from), to: timestamppb.New(to), wantFrom: &from, wantTo: &to},
		{name: "equal inclusive bounds", from: timestamppb.New(from), to: timestamppb.New(from), wantFrom: &from, wantTo: &from},
		{name: "reversed interval", from: timestamppb.New(to), to: timestamppb.New(from), wantError: true},
		{name: "invalid from", from: &timestamppb.Timestamp{Seconds: 253402300800}, wantError: true},
		{name: "invalid to", to: &timestamppb.Timestamp{Nanos: -1}, wantError: true},
		{name: "sub-millisecond precision", from: &timestamppb.Timestamp{Nanos: 1}, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFrom, gotTo, err := listRunsStartedRange(tt.from, tt.to)
			if !tt.wantError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantError && connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("error code = %v, want %v (err=%v)", connect.CodeOf(err), connect.CodeInvalidArgument, err)
			}
			if tt.wantError {
				return
			}
			assertOptionalInstant(t, "from", gotFrom, tt.wantFrom)
			assertOptionalInstant(t, "to", gotTo, tt.wantTo)
		})
	}
}

func assertOptionalInstant(t *testing.T, name string, got, want *time.Time) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
		return
	}
	if !got.Equal(*want) {
		t.Fatalf("%s = %v, want instant %v", name, got, want)
	}
}

package api

import (
	"fmt"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func listRunsStartedRange(from, to *timestamppb.Timestamp) (*time.Time, *time.Time, error) {
	startedFrom, err := validOptionalTimestamp("started_from", from)
	if err != nil {
		return nil, nil, err
	}
	startedTo, err := validOptionalTimestamp("started_to", to)
	if err != nil {
		return nil, nil, err
	}
	if startedFrom != nil && startedTo != nil && startedFrom.After(*startedTo) {
		return nil, nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("started_from must not be later than started_to"))
	}
	return startedFrom, startedTo, nil
}

func validOptionalTimestamp(name string, value *timestamppb.Timestamp) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	if err := value.CheckValid(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid %s: %w", name, err))
	}
	if value.GetNanos()%int32(time.Millisecond) != 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid %s: sub-millisecond precision is not supported", name))
	}
	instant := value.AsTime()
	return &instant, nil
}

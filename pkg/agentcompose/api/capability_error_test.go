package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"agent-compose/pkg/capability"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestCapabilityConnectErrorMapsOctoBusHTTPStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want connect.Code
	}{
		{
			name: "not found",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusNotFound, Code: "NOT_FOUND", Message: "capset not found"},
			want: connect.CodeNotFound,
		},
		{
			name: "unauthenticated",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusUnauthorized, Code: "UNAUTHENTICATED", Message: "admin token is required"},
			want: connect.CodeUnauthenticated,
		},
		{
			name: "invalid argument",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusBadRequest, Code: "INVALID_ARGUMENT", Message: "bad catalog query"},
			want: connect.CodeInvalidArgument,
		},
		{
			name: "http unauthorized fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusUnauthorized, Message: "admin token is required"},
			want: connect.CodeUnauthenticated,
		},
		{
			name: "http forbidden fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusForbidden, Message: "forbidden"},
			want: connect.CodePermissionDenied,
		},
		{
			name: "http not found fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusNotFound, Message: "missing"},
			want: connect.CodeNotFound,
		},
		{
			name: "http conflict fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusConflict, Message: "already exists"},
			want: connect.CodeAborted,
		},
		{
			name: "http precondition failed fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusPreconditionFailed, Message: "precondition failed"},
			want: connect.CodeFailedPrecondition,
		},
		{
			name: "http unprocessable entity fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusUnprocessableEntity, Message: "invalid state"},
			want: connect.CodeFailedPrecondition,
		},
		{
			name: "http too many requests fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusTooManyRequests, Message: "rate limited"},
			want: connect.CodeResourceExhausted,
		},
		{
			name: "http not implemented fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusNotImplemented, Message: "not implemented"},
			want: connect.CodeUnimplemented,
		},
		{
			name: "http gateway timeout fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusGatewayTimeout, Message: "timeout"},
			want: connect.CodeDeadlineExceeded,
		},
		{
			name: "unknown http status fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusInternalServerError, Message: "upstream failed"},
			want: connect.CodeUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CapabilityConnectError(tc.err)
			if connect.CodeOf(err) != tc.want {
				t.Fatalf("CapabilityConnectError code = %s, want %s; err=%v", connect.CodeOf(err), tc.want, err)
			}
			if !strings.Contains(err.Error(), tc.err.(*capability.OctoBusError).Message) {
				t.Fatalf("CapabilityConnectError did not preserve upstream message: %v", err)
			}
		})
	}
}

func TestCapabilityConnectErrorPrefersOctoBusCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want connect.Code
	}{
		{
			name: "deadline exceeded code without matching http fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusInternalServerError, Code: "DEADLINE_EXCEEDED", Message: "upstream timed out"},
			want: connect.CodeDeadlineExceeded,
		},
		{
			name: "aborted code without matching http fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusInternalServerError, Code: "ABORTED", Message: "write conflict"},
			want: connect.CodeAborted,
		},
		{
			name: "canceled code without matching http fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusInternalServerError, Code: "CANCELED", Message: "request canceled"},
			want: connect.CodeCanceled,
		},
		{
			name: "code overrides http fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusNotFound, Code: "ABORTED", Message: "transaction conflict"},
			want: connect.CodeAborted,
		},
		{
			name: "unavailable code overrides http fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusNotFound, Code: "UNAVAILABLE", Message: "upstream unavailable"},
			want: connect.CodeUnavailable,
		},
		{
			name: "internal code overrides http fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusNotFound, Code: "INTERNAL", Message: "upstream internal error"},
			want: connect.CodeInternal,
		},
		{
			name: "out of range code overrides http fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusNotFound, Code: "OUT_OF_RANGE", Message: "value out of range"},
			want: connect.CodeOutOfRange,
		},
		{
			name: "data loss code overrides http fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusNotFound, Code: "DATA_LOSS", Message: "upstream data loss"},
			want: connect.CodeDataLoss,
		},
		{
			name: "unknown code overrides http fallback",
			err:  &capability.OctoBusError{HTTPStatus: http.StatusNotFound, Code: "UNKNOWN", Message: "unknown upstream error"},
			want: connect.CodeUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CapabilityConnectError(tc.err)
			if connect.CodeOf(err) != tc.want {
				t.Fatalf("CapabilityConnectError code = %s, want %s; err=%v", connect.CodeOf(err), tc.want, err)
			}
			if !strings.Contains(err.Error(), tc.err.(*capability.OctoBusError).Message) {
				t.Fatalf("CapabilityConnectError did not preserve upstream message: %v", err)
			}
		})
	}
}

func TestListCapabilitySetsPreservesOctoBusErrorCode(t *testing.T) {
	upstream := &capability.OctoBusError{
		HTTPStatus: http.StatusUnauthorized,
		Code:       "UNAUTHENTICATED",
		Message:    "admin token is required",
	}
	handler := NewCapabilityV2Handler(&capabilityErrorProvider{listErr: upstream}, nil)

	_, err := handler.ListCapabilitySets(context.Background(), connect.NewRequest(&agentcomposev2.ListCapabilitySetsRequest{}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("ListCapabilitySets code = %s, want %s; err=%v", connect.CodeOf(err), connect.CodeUnauthenticated, err)
	}
	if !strings.Contains(err.Error(), upstream.Message) {
		t.Fatalf("ListCapabilitySets did not preserve upstream message: %v", err)
	}
}

type capabilityErrorProvider struct {
	listErr error
}

func (p *capabilityErrorProvider) Status(context.Context) capability.Status {
	return capability.Status{}
}

func (p *capabilityErrorProvider) ListCapsets(context.Context) ([]capability.Capset, error) {
	return nil, p.listErr
}

func (p *capabilityErrorProvider) Catalog(context.Context, string) (capability.Catalog, error) {
	return capability.Catalog{}, nil
}

func (p *capabilityErrorProvider) CapabilityGuide(context.Context, string) ([]byte, error) {
	return nil, nil
}

func (p *capabilityErrorProvider) ProxyTarget() string { return "" }

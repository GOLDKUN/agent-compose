package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/labstack/echo/v4"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

func TestDaemonTrustedHeadersMiddleware(t *testing.T) {
	tests := []struct {
		name        string
		headers     map[string]string
		wantHeaders []domain.TrustedHeader
		wantStatus  int
	}{
		{
			name:       "no mpi headers",
			headers:    map[string]string{"X-Custom": "val"},
			wantStatus: http.StatusOK,
		},
		{
			name:        "single mpi header",
			headers:     map[string]string{"X-Mpi-User-Id": "u1"},
			wantHeaders: []domain.TrustedHeader{{Name: "x-mpi-user-id", Value: "u1"}},
			wantStatus:  http.StatusOK,
		},
		{
			name: "multiple mpi headers",
			headers: map[string]string{
				"X-Mpi-User-Id":  "u1",
				"X-Mpi-Username": "alice",
			},
			wantHeaders: []domain.TrustedHeader{
				{Name: "x-mpi-user-id", Value: "u1"},
				{Name: "x-mpi-username", Value: "alice"},
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "non-mpi prefix ignored",
			headers:    map[string]string{"X-Mpi": "val"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "control char rejected",
			headers:    map[string]string{"X-Mpi-User-Id": "u\x001"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty value rejected",
			headers:    map[string]string{"X-Mpi-Empty": "  "},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "value over 256 bytes rejected",
			headers:    map[string]string{"X-Mpi-Long": string(make([]byte, 257))},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing suffix rejected",
			headers:    map[string]string{"X-Mpi-": "value"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "grpc invalid suffix rejected",
			headers:    map[string]string{"X-Mpi-Role!": "admin"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "non ascii value rejected",
			headers:    map[string]string{"X-Mpi-Username": "张三"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:        "mpi header value is trimmed",
			headers:     map[string]string{"X-Mpi-Role": " admin ", "X-Other": "x"},
			wantHeaders: []domain.TrustedHeader{{Name: "x-mpi-role", Value: "admin"}},
			wantStatus:  http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := echo.New()
			app.Use(newDaemonTrustedHeadersMiddleware())
			app.Any("/*", func(c echo.Context) error {
				got := domain.TrustedHeadersFromContext(c.Request().Context())
				if !reflect.DeepEqual(got, test.wantHeaders) {
					t.Errorf("trusted headers = %v, want %v", got, test.wantHeaders)
				}
				return c.NoContent(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range test.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, test.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestTranslateTrustedHeadersEmpty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Custom", "val")
	headers, err := translateTrustedHeaders(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(headers) != 0 {
		t.Fatalf("expected empty trusted headers, got %v", headers)
	}
}

func TestTranslateTrustedHeadersPreservesRepeatedValues(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header["X-Mpi-Role"] = []string{"admin", "auditor"}
	headers, err := translateTrustedHeaders(req)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.TrustedHeader{
		{Name: "x-mpi-role", Value: "admin"},
		{Name: "x-mpi-role", Value: "auditor"},
	}
	if !reflect.DeepEqual(headers, want) {
		t.Fatalf("trusted headers = %#v, want %#v", headers, want)
	}
}

func TestValidateTrustedHeaderValue(t *testing.T) {
	tests := []struct {
		value string
		ok    bool
	}{
		{"hello", true},
		{"", false},
		{"  ", false},
		{string(make([]byte, 257)), false},
		{"a\x00b", false},
		{"a\x1fb", false},
		{"a\x7fb", false},
		{"a\x80b", false},
		{"张三", false},
		{"a\nb", false},
		{"~", true},
	}
	for _, test := range tests {
		err := validateTrustedHeaderValue(test.value)
		if (err == nil) != test.ok {
			t.Errorf("validateTrustedHeaderValue(%q) = %v, want ok=%v", test.value, err, test.ok)
		}
	}
}

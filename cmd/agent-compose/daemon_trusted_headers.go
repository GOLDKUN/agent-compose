package main

import (
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

const xMPIPrefix = "x-mpi-"

// newDaemonTrustedHeadersMiddleware reads x-mpi-* headers injected by the
// deployment's trusted ingress, validates values, and stores them in the
// request context. The ingress must prevent direct daemon access and strip
// client-supplied x-mpi-* values before injecting its own.
func newDaemonTrustedHeadersMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			headers, err := translateTrustedHeaders(c.Request())
			if err != nil {
				return err
			}
			if len(headers) == 0 {
				return next(c)
			}
			ctx := domain.NewContextWithTrustedHeaders(c.Request().Context(), headers)
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	}
}

func translateTrustedHeaders(r *http.Request) ([]domain.TrustedHeader, error) {
	names := make([]string, 0, len(r.Header))
	for name := range r.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, xMPIPrefix) {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})

	var out []domain.TrustedHeader
	for _, name := range names {
		lower := strings.ToLower(name)
		if err := validateTrustedHeaderName(lower); err != nil {
			return nil, err
		}
		for _, val := range r.Header.Values(name) {
			val = strings.TrimSpace(val)
			if err := validateTrustedHeaderValue(val); err != nil {
				return nil, err
			}
			out = append(out, domain.TrustedHeader{Name: lower, Value: val})
		}
	}
	return out, nil
}

func validateTrustedHeaderName(name string) error {
	suffix := strings.TrimPrefix(strings.ToLower(name), xMPIPrefix)
	if suffix == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "trusted header name must include a suffix")
	}
	for i := 0; i < len(suffix); i++ {
		b := suffix[i]
		if (b < 'a' || b > 'z') && (b < '0' || b > '9') && b != '-' && b != '_' && b != '.' {
			return echo.NewHTTPError(http.StatusBadRequest, "trusted header name contains characters unsupported by gRPC metadata")
		}
	}
	return nil
}

func validateTrustedHeaderValue(value string) error {
	if strings.TrimSpace(value) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "trusted header value must not be empty")
	}
	if len(value) < 1 || len(value) > 256 {
		return echo.NewHTTPError(http.StatusBadRequest, "trusted header value must be 1-256 bytes")
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b < 0x20 || b > 0x7e {
			return echo.NewHTTPError(http.StatusBadRequest, "trusted header value must contain printable ASCII only")
		}
	}
	return nil
}

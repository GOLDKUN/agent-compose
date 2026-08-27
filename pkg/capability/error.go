package capability

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// OctoBusError preserves structured errors returned by the OctoBus admin API.
type OctoBusError struct {
	HTTPStatus int
	Code       string
	Message    string
	Details    json.RawMessage
}

func (e *OctoBusError) Error() string {
	if e == nil {
		return ""
	}
	code := strings.TrimSpace(e.Code)
	message := strings.TrimSpace(e.Message)
	switch {
	case code != "" && message != "":
		return fmt.Sprintf("octobus returned HTTP %d: %s: %s", e.HTTPStatus, code, message)
	case message != "":
		return fmt.Sprintf("octobus returned HTTP %d: %s", e.HTTPStatus, message)
	case code != "":
		return fmt.Sprintf("octobus returned HTTP %d: %s", e.HTTPStatus, code)
	default:
		return fmt.Sprintf("octobus returned HTTP %d", e.HTTPStatus)
	}
}

func octobusHTTPError(status int, body []byte) error {
	upstream := &OctoBusError{HTTPStatus: status, Code: fmt.Sprintf("HTTP_%d", status), Message: http.StatusText(status)}
	var payload struct {
		Error struct {
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Details json.RawMessage `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		if code := strings.TrimSpace(payload.Error.Code); code != "" {
			upstream.Code = code
		}
		if message := strings.TrimSpace(payload.Error.Message); message != "" {
			upstream.Message = message
		}
		if len(payload.Error.Details) > 0 {
			upstream.Details = append(json.RawMessage(nil), payload.Error.Details...)
		}
	}
	return upstream
}

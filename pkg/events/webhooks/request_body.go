package webhooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	domain "github.com/chaitin/agent-compose/pkg/model"
)

type requestBodyFormat uint8

const (
	requestBodyFormatUnsupported requestBodyFormat = iota
	requestBodyFormatJSON
	requestBodyFormatForm
)

func detectRequestBodyFormat(r *http.Request) requestBodyFormat {
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" {
		return requestBodyFormatUnsupported
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return requestBodyFormatUnsupported
	}
	switch {
	case strings.EqualFold(mediaType, "application/json"):
		return requestBodyFormatJSON
	case strings.EqualFold(mediaType, "application/x-www-form-urlencoded"):
		return requestBodyFormatForm
	default:
		return requestBodyFormatUnsupported
	}
}

func RequestContentTypeIsJSON(r *http.Request) bool {
	return detectRequestBodyFormat(r) == requestBodyFormatJSON
}

func decodeGitHubFormPayload(raw []byte) ([]byte, error) {
	values, err := url.ParseQuery(string(raw))
	if err != nil {
		return nil, fmt.Errorf("body must be valid form data")
	}
	payloads := values["payload"]
	if len(payloads) != 1 || strings.TrimSpace(payloads[0]) == "" {
		return nil, fmt.Errorf("body must contain exactly one non-empty payload form field")
	}
	return []byte(payloads[0]), nil
}

func DecodeJSONObject(raw []byte) (map[string]any, string, error) {
	var body map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		return nil, "", fmt.Errorf("body must be valid JSON")
	}
	if body == nil {
		return nil, "", fmt.Errorf("body must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, "", fmt.Errorf("body must contain one JSON document")
	}
	compact, err := domain.MarshalJSONCompact(body)
	if err != nil {
		return nil, "", err
	}
	return body, compact, nil
}

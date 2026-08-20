package llms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type GenerateRequest struct {
	Endpoint         string
	Protocol         string
	Prompt           string
	Model            string
	OutputSchemaJSON string
	Headers          http.Header
	MaxOutputTokens  int
}

type GenerateResult struct {
	Text         string
	Model        string
	ResponseID   string
	FinishReason string
}

type apiRequest struct {
	Model           string          `json:"model"`
	Input           string          `json:"input"`
	Text            *apiTextOptions `json:"text,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
}

type apiTextOptions struct {
	Format apiTextFormat `json:"format"`
}

type apiTextFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}

type apiResponse struct {
	ID                string            `json:"id"`
	Model             string            `json:"model"`
	Status            string            `json:"status"`
	OutputText        string            `json:"output_text"`
	Output            []apiOutput       `json:"output"`
	Error             *apiError         `json:"error"`
	IncompleteDetails *incompleteReason `json:"incomplete_details"`
}

type apiOutput struct {
	Type         string          `json:"type"`
	Status       string          `json:"status"`
	FinishReason string          `json:"finish_reason"`
	StopReason   string          `json:"stop_reason"`
	Content      []apiOutputText `json:"content"`
}

type apiOutputText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type incompleteReason struct {
	Reason string `json:"reason"`
}

type chatCompletionsRequest struct {
	Model               string                     `json:"model"`
	Messages            []chatCompletionsMessage   `json:"messages"`
	ResponseFormat      *chatCompletionsJSONFormat `json:"response_format,omitempty"`
	MaxCompletionTokens int                        `json:"max_completion_tokens,omitempty"`
}

type chatCompletionsMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionsJSONFormat struct {
	Type string `json:"type"`
}

type chatCompletionsResponse struct {
	ID      string                  `json:"id"`
	Model   string                  `json:"model"`
	Choices []chatCompletionsChoice `json:"choices"`
	Error   *apiError               `json:"error"`
}

type chatCompletionsChoice struct {
	Message      chatCompletionsMessage `json:"message"`
	FinishReason string                 `json:"finish_reason"`
}

type messagesRequest struct {
	Model     string                   `json:"model"`
	Messages  []messagesRequestMessage `json:"messages"`
	System    string                   `json:"system,omitempty"`
	MaxTokens int                      `json:"max_tokens"`
}

type messagesRequestMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type messagesResponse struct {
	ID         string                    `json:"id"`
	Model      string                    `json:"model"`
	StopReason string                    `json:"stop_reason"`
	Content    []messagesResponseContent `json:"content"`
	Error      *apiError                 `json:"error"`
}
type messagesResponseContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func Generate(ctx context.Context, client *http.Client, req GenerateRequest) (GenerateResult, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return GenerateResult{}, fmt.Errorf("prompt is required")
	}
	endpoint := strings.TrimSpace(req.Endpoint)
	if endpoint == "" {
		return GenerateResult{}, fmt.Errorf("llm api endpoint is not configured")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return GenerateResult{}, fmt.Errorf("llm model is required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	switch NormalizeWireAPI(req.Protocol) {
	case APIProtocolChatCompletions:
		return generateChatCompletions(ctx, generateCallParams{Client: client, Endpoint: endpoint, Prompt: prompt, Model: model, OutputSchemaJSON: req.OutputSchemaJSON, Headers: req.Headers, MaxOutputTokens: req.MaxOutputTokens})
	case APIProtocolResponses:
		return generateResponses(ctx, generateCallParams{Client: client, Endpoint: endpoint, Prompt: prompt, Model: model, OutputSchemaJSON: req.OutputSchemaJSON, Headers: req.Headers, MaxOutputTokens: req.MaxOutputTokens})
	case APIProtocolMessages:
		return generateMessages(ctx, generateCallParams{Client: client, Endpoint: endpoint, Prompt: prompt, Model: model, OutputSchemaJSON: req.OutputSchemaJSON, Headers: req.Headers, MaxOutputTokens: req.MaxOutputTokens})
	default:
		return GenerateResult{}, fmt.Errorf("unsupported llm api protocol %q", NormalizeWireAPI(req.Protocol))
	}
}

// generateCallParams groups the wire-call inputs shared by generateMessages,
// generateResponses, and generateChatCompletions.
type generateCallParams struct {
	Client           *http.Client
	Endpoint         string
	Prompt           string
	Model            string
	OutputSchemaJSON string
	Headers          http.Header
	MaxOutputTokens  int
}

func generateMessages(ctx context.Context, p generateCallParams) (GenerateResult, error) {
	client, endpoint, prompt, model, outputSchemaJSON, headers, maxOutputTokens := p.Client, p.Endpoint, p.Prompt, p.Model, p.OutputSchemaJSON, p.Headers, p.MaxOutputTokens
	if maxOutputTokens <= 0 {
		maxOutputTokens = 4096
	}
	request := messagesRequest{Model: model, Messages: []messagesRequestMessage{{Role: "user", Content: prompt}}, MaxTokens: maxOutputTokens}
	if schema := strings.TrimSpace(outputSchemaJSON); schema != "" {
		if !json.Valid([]byte(schema)) {
			return GenerateResult{}, fmt.Errorf("llm outputSchema must be valid JSON")
		}
		request.System = "Respond with a single JSON object that conforms to this JSON Schema:\n" + schema
	}
	requestBody, err := json.Marshal(request)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("encode llm messages request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return GenerateResult{}, fmt.Errorf("create llm messages request: %w", err)
	}
	ApplyForwardHeaders(httpReq.Header, headers)
	resp, err := client.Do(httpReq)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("call llm messages endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return GenerateResult{}, fmt.Errorf("read llm messages response: %w", err)
	}
	var parsed messagesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return GenerateResult{}, fmt.Errorf("llm messages endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
		return GenerateResult{}, fmt.Errorf("decode llm messages response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			message = strings.TrimSpace(parsed.Error.Message)
		}
		return GenerateResult{}, fmt.Errorf("llm messages endpoint returned %s: %s", resp.Status, message)
	}
	parts := make([]string, 0, len(parsed.Content))
	for _, content := range parsed.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			parts = append(parts, strings.TrimSpace(content.Text))
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		return GenerateResult{}, fmt.Errorf("llm messages response did not contain text output")
	}
	if strings.TrimSpace(outputSchemaJSON) != "" && !json.Valid([]byte(text)) {
		return GenerateResult{}, fmt.Errorf("llm messages response did not contain valid JSON")
	}
	return GenerateResult{Text: text, Model: firstNonEmpty(strings.TrimSpace(parsed.Model), model), ResponseID: strings.TrimSpace(parsed.ID), FinishReason: strings.TrimSpace(parsed.StopReason)}, nil
}

func generateResponses(ctx context.Context, p generateCallParams) (GenerateResult, error) {
	client, endpoint, prompt, model, outputSchemaJSON, headers, maxOutputTokens := p.Client, p.Endpoint, p.Prompt, p.Model, p.OutputSchemaJSON, p.Headers, p.MaxOutputTokens
	request := apiRequest{Model: model, Input: prompt, MaxOutputTokens: maxOutputTokens}
	if schema := strings.TrimSpace(outputSchemaJSON); schema != "" {
		if !json.Valid([]byte(schema)) {
			return GenerateResult{}, fmt.Errorf("llm outputSchema must be valid JSON")
		}
		request.Text = &apiTextOptions{
			Format: apiTextFormat{
				Type:   "json_schema",
				Name:   "agent_compose_llm_output",
				Schema: json.RawMessage(schema),
				Strict: true,
			},
		}
	}
	requestBody, err := json.Marshal(request)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("encode llm request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return GenerateResult{}, fmt.Errorf("create llm request: %w", err)
	}
	ApplyForwardHeaders(httpReq.Header, headers)
	resp, err := client.Do(httpReq)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("call llm endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return GenerateResult{}, fmt.Errorf("read llm response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		var failure apiResponse
		if err := json.Unmarshal(body, &failure); err == nil && failure.Error != nil && strings.TrimSpace(failure.Error.Message) != "" {
			message = strings.TrimSpace(failure.Error.Message)
		}
		if message == "" {
			message = fmt.Sprintf("llm endpoint returned %s", resp.Status)
		}
		return GenerateResult{}, fmt.Errorf("llm endpoint returned %s: %s", resp.Status, message)
	}
	var parsed apiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return GenerateResult{}, fmt.Errorf("decode llm response: %w", err)
	}
	text := extractResponseText(parsed)
	if text == "" {
		return GenerateResult{}, fmt.Errorf("llm response did not contain text output")
	}
	return GenerateResult{
		Text:         text,
		Model:        firstNonEmpty(strings.TrimSpace(parsed.Model), model),
		ResponseID:   strings.TrimSpace(parsed.ID),
		FinishReason: extractFinishReason(parsed),
	}, nil
}

// generateChatCompletions calls an OpenAI-compatible Chat Completions backend
// for unary prompt-to-response text generation.
func generateChatCompletions(ctx context.Context, p generateCallParams) (GenerateResult, error) {
	client, endpoint, prompt, model, outputSchemaJSON, headers, maxOutputTokens := p.Client, p.Endpoint, p.Prompt, p.Model, p.OutputSchemaJSON, p.Headers, p.MaxOutputTokens
	messages := []chatCompletionsMessage{{Role: "user", Content: prompt}}
	request := chatCompletionsRequest{Model: model, Messages: messages, MaxCompletionTokens: maxOutputTokens}
	if schema := strings.TrimSpace(outputSchemaJSON); schema != "" {
		if !json.Valid([]byte(schema)) {
			return GenerateResult{}, fmt.Errorf("llm outputSchema must be valid JSON")
		}
		request.Messages = append([]chatCompletionsMessage{{
			Role:    "system",
			Content: "Respond with a single JSON object that conforms to this JSON Schema:\n" + schema,
		}}, request.Messages...)
		request.ResponseFormat = &chatCompletionsJSONFormat{Type: "json_object"}
	}
	requestBody, err := json.Marshal(request)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("encode llm chat completions request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return GenerateResult{}, fmt.Errorf("create llm chat completions request: %w", err)
	}
	ApplyForwardHeaders(httpReq.Header, headers)
	resp, err := client.Do(httpReq)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("call llm chat completions endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return GenerateResult{}, fmt.Errorf("read llm chat completions response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		var failure chatCompletionsResponse
		if err := json.Unmarshal(body, &failure); err == nil && failure.Error != nil && strings.TrimSpace(failure.Error.Message) != "" {
			message = strings.TrimSpace(failure.Error.Message)
		}
		if message == "" {
			message = fmt.Sprintf("llm chat completions endpoint returned %s", resp.Status)
		}
		return GenerateResult{}, fmt.Errorf("llm chat completions endpoint returned %s: %s", resp.Status, message)
	}
	var parsed chatCompletionsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return GenerateResult{}, fmt.Errorf("decode llm chat completions response: %w", err)
	}
	text := extractChatCompletionsText(parsed)
	if text == "" {
		return GenerateResult{}, fmt.Errorf("llm chat completions response did not contain text output")
	}
	if strings.TrimSpace(outputSchemaJSON) != "" && !json.Valid([]byte(text)) {
		return GenerateResult{}, fmt.Errorf("llm chat completions response did not contain valid JSON")
	}
	return GenerateResult{
		Text:         text,
		Model:        firstNonEmpty(strings.TrimSpace(parsed.Model), model),
		ResponseID:   strings.TrimSpace(parsed.ID),
		FinishReason: extractChatCompletionsFinishReason(parsed),
	}, nil
}

func ApplyForwardHeaders(dst http.Header, src http.Header) {
	dst.Set("Content-Type", "application/json")
	for key, values := range src {
		for _, value := range values {
			dst.Set(key, value)
		}
	}
}

func extractResponseText(response apiResponse) string {
	if text := strings.TrimSpace(response.OutputText); text != "" {
		return text
	}
	parts := make([]string, 0)
	for _, item := range response.Output {
		for _, content := range item.Content {
			text := strings.TrimSpace(content.Text)
			if text == "" {
				continue
			}
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func extractFinishReason(response apiResponse) string {
	if response.IncompleteDetails != nil {
		if reason := strings.TrimSpace(response.IncompleteDetails.Reason); reason != "" {
			return reason
		}
	}
	for _, item := range response.Output {
		if reason := strings.TrimSpace(item.FinishReason); reason != "" {
			return reason
		}
		if reason := strings.TrimSpace(item.StopReason); reason != "" {
			return reason
		}
	}
	return strings.TrimSpace(response.Status)
}

func extractChatCompletionsText(response chatCompletionsResponse) string {
	parts := make([]string, 0, len(response.Choices))
	for _, choice := range response.Choices {
		text := strings.TrimSpace(choice.Message.Content)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func extractChatCompletionsFinishReason(response chatCompletionsResponse) string {
	for _, choice := range response.Choices {
		if reason := strings.TrimSpace(choice.FinishReason); reason != "" {
			return reason
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

package mimo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"mimo-tui/internal/config"
	"mimo-tui/internal/core"
	"mimo-tui/internal/model"
)

const (
	DefaultBaseURL         = model.DefaultMiMoBaseURL
	DefaultModel           = "mimo-v2.5-pro"
	DefaultMultimodalModel = "mimo-v2.5"
)

type Client struct {
	baseURL   string
	apiKey    string
	model     string
	mock      bool
	http      *http.Client
	modelInfo model.Info

	webSearchDisabled atomic.Bool
}

func New(cfg config.ProviderConfig) *Client {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = DefaultModel
	}

	return &Client{
		baseURL: baseURL,
		apiKey:  strings.TrimSpace(cfg.APIKey),
		model:   model,
		mock:    cfg.Mock || strings.TrimSpace(cfg.APIKey) == "",
		http: &http.Client{
			Transport: &http.Transport{
				DialContext:         (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
				TLSHandshakeTimeout: 10 * time.Second,
			},
			Timeout: 0,
		},
	}
}

func NewMock(model string) *Client {
	cfg := config.ProviderConfig{
		BaseURL: DefaultBaseURL,
		Model:   model,
		Mock:    true,
	}
	return New(cfg)
}

// SetModelInfo attaches registry-level model metadata for use by the client.
func (c *Client) SetModelInfo(info model.Info) {
	c.modelInfo = info
}

func (c *Client) ChatStream(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil {
		return nil, errors.New("mimo: nil client")
	}
	req = c.normalizeRequest(req)
	if c.mock {
		return c.mockStream(ctx, req), nil
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mimo: encode chat request: %w", err)
	}

	out := make(chan core.ModelEvent, 8)
	go func() {
		defer close(out)
		resp, err := c.doWithRetry(ctx, body)
		if err != nil {
			sendModelEvent(ctx, out, core.ModelEvent{Err: err})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			retryAfter := resp.Header.Get("Retry-After")
			bodyText := readErrorBody(resp.Body)
			if isWebSearchDisabledError(bodyText) && requestHasNonForcedWebSearch(req) {
				c.webSearchDisabled.Store(true)
				resp.Body.Close()
				fallbackReq := stripNativeWebSearch(req)
				fallbackReq = addWebSearchDisabledNote(fallbackReq)
				fallbackBody, marshalErr := json.Marshal(fallbackReq)
				if marshalErr != nil {
					sendModelEvent(ctx, out, core.ModelEvent{Err: fmt.Errorf("mimo: encode fallback chat request: %w", marshalErr)})
					return
				}
				resp, err = c.doWithRetry(ctx, fallbackBody)
				if err != nil {
					sendModelEvent(ctx, out, core.ModelEvent{Err: err})
					return
				}
				defer resp.Body.Close()
				if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
					readEventStream(ctx, resp.Body, out)
					return
				}
				retryAfter = resp.Header.Get("Retry-After")
				bodyText = readErrorBody(resp.Body)
			}
			sendModelEvent(ctx, out, core.ModelEvent{Err: formatHTTPError(resp.StatusCode, retryAfter, bodyText)})
			return
		}

		readEventStream(ctx, resp.Body, out)
	}()
	return out, nil
}

func (c *Client) normalizeRequest(req core.ChatRequest) core.ChatRequest {
	if strings.TrimSpace(req.Model) == "" {
		req.Model = c.model
	}
	if requestHasMedia(req) && !supportsMultimodalInput(req.Model) {
		req.Model = multimodalFallbackModel()
	}
	if c.webSearchDisabled.Load() && requestHasNonForcedWebSearch(req) {
		req = addWebSearchDisabledNote(stripNativeWebSearch(req))
	}
	req.Stream = true
	return req
}

func (c *Client) endpoint() string {
	base := strings.TrimRight(c.baseURL, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}

func (c *Client) mockStream(ctx context.Context, req core.ChatRequest) <-chan core.ModelEvent {
	out := make(chan core.ModelEvent, 4)
	go func() {
		defer close(out)
		for _, chunk := range c.mockChunks(req) {
			if !sendModelEvent(ctx, out, core.ModelEvent{Delta: chunk}) {
				return
			}
		}
		if !sendModelEvent(ctx, out, core.ModelEvent{Usage: c.mockUsage(req)}) {
			return
		}
		sendModelEvent(ctx, out, core.ModelEvent{Done: true})
	}()
	return out
}

func (c *Client) mockChunks(req core.ChatRequest) []string {
	prompt := lastUserMessage(req.Messages)
	if prompt == "" {
		if c.modelInfo.ContextLimit > 0 {
			return []string{fmt.Sprintf("MiMo mock response ready (context window: %d tokens).", c.modelInfo.ContextLimit)}
		}
		return []string{"MiMo mock response ready."}
	}
	chunks := []string{"MiMo mock response: "}
	if c.modelInfo.ContextLimit > 0 {
		chunks = append(chunks, fmt.Sprintf("[model=%s ctx=%d] ", c.modelInfo.ID, c.modelInfo.ContextLimit))
	}
	chunks = append(chunks, prompt)
	return chunks
}

func lastUserMessage(messages []core.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return strings.TrimSpace(messages[i].TextContent())
		}
	}
	return ""
}

func (c *Client) mockUsage(req core.ChatRequest) *core.CostUpdate {
	inputTokens := 0
	for _, msg := range req.Messages {
		inputTokens += len(strings.Fields(msg.TextContent()))
	}
	outputTokens := 0
	for _, chunk := range c.mockChunks(req) {
		outputTokens += len(strings.Fields(chunk))
	}
	return &core.CostUpdate{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
	}
}

func requestHasMedia(req core.ChatRequest) bool {
	for _, msg := range req.Messages {
		for _, part := range msg.ContentParts {
			switch part.Type {
			case "image_url", "input_audio", "video_url":
				return true
			}
		}
	}
	return false
}

func requestHasNonForcedWebSearch(req core.ChatRequest) bool {
	for _, tool := range req.Tools {
		if tool.Type == "web_search" {
			return !tool.ForceSearch
		}
	}
	return false
}

func stripNativeWebSearch(req core.ChatRequest) core.ChatRequest {
	tools := make([]core.ToolSpec, 0, len(req.Tools))
	for _, tool := range req.Tools {
		if tool.Type == "web_search" {
			continue
		}
		tools = append(tools, tool)
	}
	req.Tools = tools
	return req
}

func addWebSearchDisabledNote(req core.ChatRequest) core.ChatRequest {
	const note = "Runtime note: the MiMo API key rejected native web_search because the Web Search Plugin is not enabled for this platform account. Answer without claiming live web access, and tell the user to enable the MiMo Web Search Plugin if current online information is required."
	if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
		req.Messages[0].Content = strings.TrimSpace(req.Messages[0].Content + "\n\n" + note)
		return req
	}
	req.Messages = append([]core.Message{{Role: "system", Content: note}}, req.Messages...)
	return req
}

func isWebSearchDisabledError(body string) bool {
	body = strings.ToLower(body)
	return strings.Contains(body, "websearchenabled is false") ||
		strings.Contains(body, "web search plugin") && strings.Contains(body, "not") && strings.Contains(body, "enabled")
}

func supportsMultimodalInput(modelID string) bool {
	switch strings.TrimSpace(modelID) {
	case "mimo-v2.5", "mimo-v2-omni":
		return true
	default:
		return false
	}
}

func multimodalFallbackModel() string {
	modelID := strings.TrimSpace(configEnv("MIMO_MULTIMODAL_MODEL"))
	if modelID == "" {
		return DefaultMultimodalModel
	}
	return modelID
}

func configEnv(name string) string {
	return strings.TrimSpace(strings.Trim(os.Getenv(name), `"`))
}

func sendModelEvent(ctx context.Context, out chan<- core.ModelEvent, event core.ModelEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- event:
		return true
	}
}

func readEventStream(ctx context.Context, r io.Reader, out chan<- core.ModelEvent) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var dataLines []string
	toolCalls := newToolCallAccumulator()
	done := false
	flush := func() bool {
		if len(dataLines) == 0 {
			return false
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil
		return handleSSEData(ctx, data, out, toolCalls)
	}

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if flush() {
				done = true
				return
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimPrefix(data, " ")
			dataLines = append(dataLines, data)
		}
	}

	if flush() {
		done = true
	}
	if err := scanner.Err(); err != nil {
		sendModelEvent(ctx, out, core.ModelEvent{Err: fmt.Errorf("mimo: read event stream: %w", err)})
		return
	}
	if !done {
		if calls := toolCalls.complete(); len(calls) > 0 {
			sendModelEvent(ctx, out, core.ModelEvent{ToolCalls: calls})
		}
		sendModelEvent(ctx, out, core.ModelEvent{Done: true})
	}
}

func handleSSEData(ctx context.Context, data string, out chan<- core.ModelEvent, toolCalls *toolCallAccumulator) bool {
	if strings.TrimSpace(data) == "[DONE]" {
		if toolCalls != nil {
			if calls := toolCalls.complete(); len(calls) > 0 {
				sendModelEvent(ctx, out, core.ModelEvent{ToolCalls: calls})
			}
		}
		sendModelEvent(ctx, out, core.ModelEvent{Done: true})
		return true
	}

	var chunk streamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		sendModelEvent(ctx, out, core.ModelEvent{Err: fmt.Errorf("mimo: decode stream chunk: %w", err)})
		return true
	}
	if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
		sendModelEvent(ctx, out, core.ModelEvent{Err: fmt.Errorf("mimo: stream error: %s", parseStreamError(chunk.Error))})
		return true
	}
	for _, choice := range chunk.Choices {
		event := core.ModelEvent{Delta: choice.Delta.Content}
		if toolCalls != nil {
			toolCalls.apply(choice.Delta.ToolCalls)
		}
		if event.Delta != "" {
			sendModelEvent(ctx, out, event)
		}
	}
	if chunk.Usage != nil {
		sendModelEvent(ctx, out, core.ModelEvent{Usage: chunk.Usage.cost()})
	}
	return false
}

type streamChunk struct {
	Choices []streamChoice  `json:"choices"`
	Usage   *streamUsage    `json:"usage"`
	Error   json.RawMessage `json:"error"`
}

type streamChoice struct {
	Delta streamDelta `json:"delta"`
}

type streamDelta struct {
	Content   string                `json:"content"`
	ToolCalls []streamToolCallDelta `json:"tool_calls"`
}

type streamToolCallDelta struct {
	Index    int                     `json:"index"`
	ID       string                  `json:"id"`
	Type     string                  `json:"type"`
	Function streamToolFunctionDelta `json:"function"`
}

type streamToolFunctionDelta struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolCallAccumulator struct {
	calls map[int]*toolCallState
}

type toolCallState struct {
	id        string
	name      string
	arguments string
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{calls: make(map[int]*toolCallState)}
}

func (a *toolCallAccumulator) apply(deltas []streamToolCallDelta) {
	if len(deltas) == 0 {
		return
	}
	for _, delta := range deltas {
		state := a.calls[delta.Index]
		if state == nil {
			state = &toolCallState{}
			a.calls[delta.Index] = state
		}
		if delta.ID != "" {
			state.id = delta.ID
		}
		if delta.Function.Name != "" {
			state.name += delta.Function.Name
		}
		if delta.Function.Arguments != "" {
			state.arguments += delta.Function.Arguments
		}
	}
}

func (a *toolCallAccumulator) complete() []core.ToolCall {
	if a == nil || len(a.calls) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(a.calls))
	for index := range a.calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	out := make([]core.ToolCall, 0, len(indexes))
	for _, index := range indexes {
		state := a.calls[index]
		if state == nil || strings.TrimSpace(state.name) == "" {
			continue
		}
		out = append(out, state.toolCall())
	}
	a.calls = make(map[int]*toolCallState)
	return out
}

func (s toolCallState) toolCall() core.ToolCall {
	return core.ToolCall{
		ID:    s.id,
		Name:  s.name,
		Input: parseToolInput(s.arguments),
		Raw:   s.arguments,
	}
}

func parseToolInput(raw string) core.ToolInput {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var input core.ToolInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return nil
	}
	return input
}

type streamUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (u streamUsage) cost() *core.CostUpdate {
	total := u.TotalTokens
	if total == 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	return &core.CostUpdate{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  total,
	}
}

func parseStreamError(raw json.RawMessage) string {
	var structured struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &structured); err == nil && structured.Message != "" {
		return structured.Message
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil && text != "" {
		return text
	}
	return string(raw)
}

func readErrorBody(r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, 4096))
	if err != nil {
		return err.Error()
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "empty response body"
	}

	var apiErr struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &apiErr); err == nil && len(apiErr.Error) > 0 {
		return parseStreamError(apiErr.Error)
	}
	return text
}

// retryableStatusCodes lists HTTP status codes that trigger a retry.
var retryableStatusCodes = map[int]bool{
	http.StatusTooManyRequests:    true,
	http.StatusBadGateway:         true,
	http.StatusServiceUnavailable: true,
	http.StatusGatewayTimeout:     true,
}

// isRetryableStatusCode reports whether the HTTP status code should be retried.
func isRetryableStatusCode(code int) bool {
	return retryableStatusCodes[code]
}

// doWithRetry performs the HTTP request with retry and backoff for transient errors.
// It retries on 429, 502, 503, 504 with backoffs of 1s, 2s, 4s (max 3 retries).
// When retries are exhausted, it returns nil and a formatted error based on the
// last response's status code and body.
func (c *Client) doWithRetry(ctx context.Context, body []byte) (*http.Response, error) {
	backoffs := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	var lastStatus int
	var lastRetryAfter string
	var lastBodyText string

	for attempt := 0; attempt <= len(backoffs); attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("mimo: build chat request: %w", err)
		}
		httpReq.Header.Set("Accept", "text/event-stream")
		httpReq.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
			httpReq.Header.Set("api-key", c.apiKey)
		}

		resp, err := c.http.Do(httpReq)
		if err != nil {
			return nil, wrapConnectionError(err, c.endpoint())
		}

		if !isRetryableStatusCode(resp.StatusCode) {
			return resp, nil
		}

		// Capture error info from this response for potential later use.
		lastStatus = resp.StatusCode
		lastRetryAfter = resp.Header.Get("Retry-After")
		lastBodyText = readErrorBody(resp.Body)
		resp.Body.Close()

		if attempt < len(backoffs) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoffs[attempt]):
			}
		}
	}

	return nil, formatHTTPError(lastStatus, lastRetryAfter, lastBodyText)
}

// wrapConnectionError produces a descriptive error for network-level failures.
func wrapConnectionError(err error, url string) error {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return fmt.Errorf("mimo: Cannot reach MiMo API at %s: %w", url, err)
	}
	return fmt.Errorf("mimo: chat stream request: %w", err)
}

// formatHTTPError produces a structured, user-friendly error for HTTP status codes.
func formatHTTPError(statusCode int, retryAfter string, body string) error {
	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		if body != "" {
			return fmt.Errorf("mimo: MiMo API authentication failed. Check MIMO_API_KEY. (status %d: %s)", statusCode, body)
		}
		return fmt.Errorf("mimo: MiMo API authentication failed. Check MIMO_API_KEY. (status %d)", statusCode)
	case statusCode == http.StatusTooManyRequests:
		if retryAfter != "" {
			return fmt.Errorf("mimo: MiMo API rate limit exceeded (status %d: %s; Retry-After: %s)", statusCode, body, retryAfter)
		}
		return fmt.Errorf("mimo: MiMo API rate limit exceeded (status %d: %s)", statusCode, body)
	case statusCode == http.StatusBadGateway ||
		statusCode == http.StatusServiceUnavailable ||
		statusCode == http.StatusGatewayTimeout:
		return fmt.Errorf("mimo: MiMo API temporarily unavailable (status %d: %s)", statusCode, body)
	default:
		return fmt.Errorf("mimo: chat completions returned %d: %s", statusCode, body)
	}
}

// HealthCheck sends a minimal request to verify the API is reachable.
// Returns nil if the server responds (any status), or an error if unreachable.
func (c *Client) HealthCheck(ctx context.Context) error {
	if c.mock {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	baseURL := strings.TrimRight(c.baseURL, "/")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("mimo: health check request: %w", err)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return wrapConnectionError(err, baseURL+"/health")
	}
	defer resp.Body.Close()
	return nil
}

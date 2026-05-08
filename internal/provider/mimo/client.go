package mimo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"mimo-tui/internal/config"
	"mimo-tui/internal/core"
	"mimo-tui/internal/model"
)

const (
	DefaultBaseURL = "https://api.xiaomimimo.com/v1"
	DefaultModel   = "mimo-v2.5-pro"
)

type Client struct {
	baseURL   string
	apiKey    string
	model     string
	mock      bool
	http      *http.Client
	modelInfo model.Info
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
		http:    http.DefaultClient,
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

	out := make(chan core.ModelEvent, 8)
	go func() {
		defer close(out)
		resp, err := c.http.Do(httpReq)
		if err != nil {
			sendModelEvent(ctx, out, core.ModelEvent{Err: fmt.Errorf("mimo: chat stream request: %w", err)})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			sendModelEvent(ctx, out, core.ModelEvent{Err: fmt.Errorf("mimo: chat completions returned %s: %s", resp.Status, readErrorBody(resp.Body))})
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
			return strings.TrimSpace(messages[i].Content)
		}
	}
	return ""
}

func (c *Client) mockUsage(req core.ChatRequest) *core.CostUpdate {
	inputTokens := 0
	for _, msg := range req.Messages {
		inputTokens += len(strings.Fields(msg.Content))
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
		sendModelEvent(ctx, out, core.ModelEvent{Done: true})
	}
}

func handleSSEData(ctx context.Context, data string, out chan<- core.ModelEvent, toolCalls *toolCallAccumulator) bool {
	if strings.TrimSpace(data) == "[DONE]" {
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
			event.ToolCalls = toolCalls.apply(choice.Delta.ToolCalls)
		}
		if event.Delta != "" || len(event.ToolCalls) > 0 {
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

func (a *toolCallAccumulator) apply(deltas []streamToolCallDelta) []core.ToolCall {
	if len(deltas) == 0 {
		return nil
	}
	out := make([]core.ToolCall, 0, len(deltas))
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
		out = append(out, state.toolCall())
	}
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

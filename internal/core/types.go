package core

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type EventType string

const (
	EventMessageDelta   EventType = "message_delta"
	EventToolStart      EventType = "tool_start"
	EventToolResult     EventType = "tool_result"
	EventObservation    EventType = "observation"
	EventContextUpdate  EventType = "context_update"
	EventContextPin     EventType = "context_pin"
	EventContextUnpin   EventType = "context_unpin"
	EventContextRemove  EventType = "context_remove"
	EventRiskUpdate     EventType = "risk_update"
	EventCostUpdate     EventType = "cost_update"
	EventTraceUpdate    EventType = "trace_update"
	EventError          EventType = "error"
	EventDone           EventType = "done"
	EventApprovalNeeded EventType = "approval_needed"
	EventUserPrompt     EventType = "user_prompt"
	EventOracleReview   EventType = "oracle_review"
	EventInterrupt      EventType = "interrupt"
	EventAgentStarted   EventType = "agent_started"
	EventActivityUpdate EventType = "activity_update"
)

type ActivityKind string

const (
	ActivityTool     ActivityKind = "tool"
	ActivitySkill    ActivityKind = "skill"
	ActivityMCP      ActivityKind = "mcp"
	ActivitySubAgent ActivityKind = "subagent"
)

type ActivityStatus string

const (
	ActivityPlanned ActivityStatus = "planned"
	ActivityRunning ActivityStatus = "running"
	ActivityBlocked ActivityStatus = "blocked"
	ActivityDone    ActivityStatus = "done"
	ActivityFailed  ActivityStatus = "failed"
	ActivitySkipped ActivityStatus = "skipped"
)

// ActivityEvent makes background work visible without polluting the chat
// transcript. It is intentionally higher-level than raw tool output: stdout,
// stderr, full diffs, and large payloads should stay artifact-backed.
type ActivityEvent struct {
	ID         string         `json:"id"`
	ParentID   string         `json:"parent_id,omitempty"`
	Kind       ActivityKind   `json:"kind"`
	Name       string         `json:"name"`
	Title      string         `json:"title,omitempty"`
	Status     ActivityStatus `json:"status"`
	Summary    string         `json:"summary,omitempty"`
	Detail     string         `json:"detail,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	ServerName string         `json:"server_name,omitempty"`
	ModelName  string         `json:"model_name,omitempty"`
	Role       string         `json:"role,omitempty"`
	Files      []string       `json:"files,omitempty"`
	Artifacts  []string       `json:"artifacts,omitempty"`
	StartedAt  time.Time      `json:"started_at,omitempty"`
	EndedAt    time.Time      `json:"ended_at,omitempty"`
}

type ContextTier string

const (
	TierNear     ContextTier = "near"
	TierAnchor   ContextTier = "anchor"
	TierArtifact ContextTier = "artifact"
)

type ContextItem struct {
	ID              string      `json:"id"`
	Tier            ContextTier `json:"tier"`
	Title           string      `json:"title"`
	Source          string      `json:"source"`
	TokenEstimate   int         `json:"token_estimate"`
	Pinned          bool        `json:"pinned"`
	Reason          string      `json:"reason"`
	SelectionReason string      `json:"selection_reason,omitempty"`
	ReplacedBy      string      `json:"replaced_by,omitempty"`
	ExpiresAt       time.Time   `json:"expires_at,omitempty"`
	SourceFile      string      `json:"source_file,omitempty"`
	Keywords        []string    `json:"keywords,omitempty"`
}

// CompressionRecord describes a context-compression operation that merged
// multiple low-activity items into a single compressed summary artifact.
type CompressionRecord struct {
	ID           string   `json:"id"`
	SourceIDs    []string `json:"source_ids"`
	Summary      string   `json:"summary"`
	Reason       string   `json:"reason"`
	TokensBefore int      `json:"tokens_before"`
	TokensAfter  int      `json:"tokens_after"`
}

type ContextSnapshot struct {
	WindowTokens       int                 `json:"window_tokens"`
	UsedTokens         int                 `json:"used_tokens"`
	Items              []ContextItem       `json:"items"`
	PollutionRisk      string              `json:"pollution_risk"`
	CompressionRecords []CompressionRecord `json:"compression_records,omitempty"`
}

type TraceStepStatus string

const (
	TracePending TraceStepStatus = "pending"
	TraceRunning TraceStepStatus = "running"
	TraceDone    TraceStepStatus = "done"
	TraceFailed  TraceStepStatus = "failed"
)

type TrajectoryStage string

const (
	StageInspect TrajectoryStage = "inspect"
	StagePlan    TrajectoryStage = "plan"
	StagePatch   TrajectoryStage = "patch"
	StageTest    TrajectoryStage = "test"
	StageRevise  TrajectoryStage = "revise"
	StageSummary TrajectoryStage = "summary"
)

type TraceStep struct {
	ID          string          `json:"id"`
	Goal        string          `json:"goal"`
	Plan        string          `json:"plan"`
	Action      string          `json:"action"`
	Observation string          `json:"observation"`
	Revision    string          `json:"revision"`
	Risk        string          `json:"risk"`
	Status      TraceStepStatus `json:"status"`
	Stage       TrajectoryStage `json:"stage,omitempty"`
	StartedAt   time.Time       `json:"started_at,omitempty"`
	EndedAt     time.Time       `json:"ended_at,omitempty"`
}

type Observation struct {
	Summary            string      `json:"summary"`
	StateDelta         string      `json:"state_delta"`
	RiskDelta          string      `json:"risk_delta"`
	NextAffordances    []string    `json:"next_affordances"`
	ContextPlacement   ContextTier `json:"context_placement"`
	ArtifactID         string      `json:"artifact_id,omitempty"`
	RollbackArtifactID string      `json:"rollback_artifact_id,omitempty"`
}

type AgentEvent struct {
	Type        EventType        `json:"type"`
	Time        time.Time        `json:"time"`
	Message     string           `json:"message,omitempty"`
	ToolName    string           `json:"tool_name,omitempty"`
	ToolCall    *ToolCall        `json:"tool_call,omitempty"`
	Trace       *TraceStep       `json:"trace,omitempty"`
	Context     *ContextSnapshot `json:"context,omitempty"`
	Observation *Observation     `json:"observation,omitempty"`
	Cost        *CostUpdate      `json:"cost,omitempty"`
	Activity    *ActivityEvent   `json:"activity,omitempty"`
	Err         string           `json:"error,omitempty"`
	Approval    *ApprovalRequest `json:"-"`
}

type CostUpdate struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	EstimatedUSD float64 `json:"estimated_usd"`
}

type Message struct {
	Role         string        `json:"role"`
	Content      string        `json:"content"`
	ContentParts []ContentPart `json:"-"`
	ToolCallID   string        `json:"tool_call_id,omitempty"`
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
}

type ContentPart struct {
	Type            string          `json:"type"`
	Text            string          `json:"text,omitempty"`
	ImageURL        *ImageURLPart   `json:"image_url,omitempty"`
	InputAudio      *InputAudioPart `json:"input_audio,omitempty"`
	VideoURL        *VideoURLPart   `json:"video_url,omitempty"`
	FPS             float64         `json:"fps,omitempty"`
	MediaResolution string          `json:"media_resolution,omitempty"`
}

type ImageURLPart struct {
	URL string `json:"url"`
}

type InputAudioPart struct {
	Data string `json:"data"`
}

type VideoURLPart struct {
	URL string `json:"url"`
}

func (m Message) TextContent() string {
	if strings.TrimSpace(m.Content) != "" {
		return m.Content
	}
	var parts []string
	for _, part := range m.ContentParts {
		switch part.Type {
		case "text":
			if strings.TrimSpace(part.Text) != "" {
				parts = append(parts, part.Text)
			}
		case "image_url":
			parts = append(parts, "[image]")
		case "input_audio":
			parts = append(parts, "[audio]")
		case "video_url":
			parts = append(parts, "[video]")
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func (m Message) MarshalJSON() ([]byte, error) {
	content := any(m.Content)
	if len(m.ContentParts) > 0 {
		content = m.ContentParts
	}
	out := map[string]any{
		"role":    m.Role,
		"content": content,
	}
	if m.ToolCallID != "" {
		out["tool_call_id"] = m.ToolCallID
	}
	if len(m.ToolCalls) > 0 {
		out["tool_calls"] = m.ToolCalls
	}
	return json.Marshal(out)
}

type ChatRequest struct {
	Model       string     `json:"model"`
	Messages    []Message  `json:"messages"`
	Stream      bool       `json:"stream"`
	Temperature float64    `json:"temperature,omitempty"`
	TopP        float64    `json:"top_p,omitempty"`
	Tools       []ToolSpec `json:"tools,omitempty"`
	ToolChoice  any        `json:"tool_choice,omitempty"`
}

type ModelEvent struct {
	Delta     string
	ToolCalls []ToolCall
	Usage     *CostUpdate
	Done      bool
	Err       error
}

type ToolCall struct {
	ID    string    `json:"id,omitempty"`
	Name  string    `json:"name"`
	Input ToolInput `json:"input,omitempty"`
	Raw   string    `json:"raw,omitempty"`
}

// MarshalJSON serializes ToolCall in OpenAI-compatible format:
// {"id":"...","type":"function","function":{"name":"...","arguments":"..."}}
func (tc ToolCall) MarshalJSON() ([]byte, error) {
	args := tc.Raw
	if args == "" && tc.Input != nil {
		b, _ := json.Marshal(tc.Input)
		args = string(b)
	}
	if args == "" {
		args = "{}"
	}
	m := map[string]any{
		"type": "function",
		"function": map[string]string{
			"name":      tc.Name,
			"arguments": args,
		},
	}
	if tc.ID != "" {
		m["id"] = tc.ID
	}
	return json.Marshal(m)
}

// UnmarshalJSON deserializes ToolCall from OpenAI-compatible format.
func (tc *ToolCall) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	tc.ID = raw.ID
	tc.Name = raw.Function.Name
	tc.Raw = raw.Function.Arguments
	if tc.Raw != "" {
		var input ToolInput
		if err := json.Unmarshal([]byte(tc.Raw), &input); err == nil {
			tc.Input = input
		}
	}
	return nil
}

type ToolSpec struct {
	Type         string             `json:"type"`
	Function     ToolFunctionSpec   `json:"function,omitempty"`
	MaxKeyword   int                `json:"max_keyword,omitempty"`
	ForceSearch  bool               `json:"force_search,omitempty"`
	Limit        int                `json:"limit,omitempty"`
	UserLocation *WebSearchLocation `json:"user_location,omitempty"`
}

type WebSearchLocation struct {
	Type    string `json:"type"`
	Country string `json:"country,omitempty"`
	Region  string `json:"region,omitempty"`
	City    string `json:"city,omitempty"`
}

func (s ToolSpec) MarshalJSON() ([]byte, error) {
	if s.Type == "" || s.Type == "function" || s.Function.Name != "" {
		return json.Marshal(map[string]any{
			"type":     "function",
			"function": s.Function,
		})
	}
	out := map[string]any{"type": s.Type}
	if s.MaxKeyword > 0 {
		out["max_keyword"] = s.MaxKeyword
	}
	if s.ForceSearch {
		out["force_search"] = true
	}
	if s.Limit > 0 {
		out["limit"] = s.Limit
	}
	if s.UserLocation != nil {
		out["user_location"] = s.UserLocation
	}
	return json.Marshal(out)
}

type ToolFunctionSpec struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Parameters  JSONSchema `json:"parameters,omitempty"`
}

type ModelClient interface {
	ChatStream(ctx context.Context, req ChatRequest) (<-chan ModelEvent, error)
}

type JSONSchema map[string]any

type PermissionBehavior string

const (
	PermissionAllow PermissionBehavior = "allow"
	PermissionAsk   PermissionBehavior = "ask"
	PermissionDeny  PermissionBehavior = "deny"
)

type PermissionRequest struct {
	Behavior PermissionBehavior `json:"behavior"`
	Reason   string             `json:"reason"`
}

type ApprovalRequest struct {
	ToolCall       ToolCall              `json:"tool_call"`
	Permission     PermissionRequest     `json:"permission"`
	TimeoutSeconds int                   `json:"timeout_seconds,omitempty"`
	Response       chan ApprovalDecision `json:"-"`
}

type ApprovalDecision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

type ToolInput map[string]any

type ToolResult struct {
	Content    string `json:"content"`
	ExitCode   int    `json:"exit_code"`
	ArtifactID string `json:"artifact_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

type SafetyGrade string

const (
	SafetyReadOnly          SafetyGrade = "read_only"
	SafetyWorkspaceMutation SafetyGrade = "workspace_mutation"
	SafetyShellMutation     SafetyGrade = "shell_mutation"
	SafetyDestructive       SafetyGrade = "destructive"
)

type Tool interface {
	Name() string
	Schema() JSONSchema
	Safety(input ToolInput) SafetyGrade
	Permission(input ToolInput) PermissionRequest
	Run(ctx context.Context, input ToolInput) ToolResult
	Summarize(result ToolResult) Observation
}

func NewEvent(t EventType) AgentEvent {
	return AgentEvent{Type: t, Time: time.Now()}
}

func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len(text) + 3) / 4
}

func MarshalSchema(v any) JSONSchema {
	raw, _ := json.Marshal(v)
	var schema JSONSchema
	_ = json.Unmarshal(raw, &schema)
	return schema
}

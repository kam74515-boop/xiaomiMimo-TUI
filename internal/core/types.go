package core

import (
	"context"
	"encoding/json"
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
)

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
}

type ContextSnapshot struct {
	WindowTokens  int           `json:"window_tokens"`
	UsedTokens    int           `json:"used_tokens"`
	Items         []ContextItem `json:"items"`
	PollutionRisk string        `json:"pollution_risk"`
}

type TraceStepStatus string

const (
	TracePending TraceStepStatus = "pending"
	TraceRunning TraceStepStatus = "running"
	TraceDone    TraceStepStatus = "done"
	TraceFailed  TraceStepStatus = "failed"
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
	StartedAt   time.Time       `json:"started_at,omitempty"`
	EndedAt     time.Time       `json:"ended_at,omitempty"`
}

type Observation struct {
	Summary          string      `json:"summary"`
	StateDelta       string      `json:"state_delta"`
	RiskDelta        string      `json:"risk_delta"`
	NextAffordances  []string    `json:"next_affordances"`
	ContextPlacement ContextTier `json:"context_placement"`
	ArtifactID       string      `json:"artifact_id,omitempty"`
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
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
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

type ToolSpec struct {
	Type     string           `json:"type"`
	Function ToolFunctionSpec `json:"function"`
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
	ToolCall   ToolCall              `json:"tool_call"`
	Permission PermissionRequest     `json:"permission"`
	Response   chan ApprovalDecision `json:"-"`
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

type Tool interface {
	Name() string
	Schema() JSONSchema
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

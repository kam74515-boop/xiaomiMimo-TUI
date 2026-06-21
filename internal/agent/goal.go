package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mimo-tui/internal/core"
)

// DefaultMaxGoalReact bounds how many times the goal gate may re-enter the agent
// loop for a single prompt before giving up, preventing unbounded token spend.
const DefaultMaxGoalReact = 12

// Verdict is the structured judgement returned by a Judge.
type Verdict struct {
	OK         bool   `json:"ok"`
	Impossible bool   `json:"impossible"`
	Reason     string `json:"reason"`
}

// Judge independently decides whether an active goal condition has been
// satisfied by the work in the transcript. It is deliberately decoupled from
// the working agent: a separate model call evaluates the conversation so the
// agent's own optimism cannot end a long-horizon task prematurely.
type Judge interface {
	Evaluate(ctx context.Context, condition string, transcript []core.Message) (Verdict, error)
}

// JudgeSystemPrompt instructs the judge to return a strict JSON verdict.
const JudgeSystemPrompt = `You are an independent completion judge for a coding agent.
You are given a STOPPING CONDITION and the full transcript of the agent's work.
Decide whether the condition is genuinely and verifiably satisfied by the work shown.

Rules:
- Do NOT accept the agent's own claim of success at face value. Look for concrete evidence in the transcript (files written, tests passing, commands succeeding).
- Set "ok": true only when the condition is truly met.
- Set "impossible": true only when the condition cannot be achieved (not merely unfinished).
- When not yet satisfied, set "ok": false and explain in "reason" precisely what is still missing, quoting transcript evidence.

Respond with ONLY a single JSON object and nothing else:
{"ok": <bool>, "impossible": <bool>, "reason": "<short explanation>"}`

// ModelJudge implements Judge using a core.ModelClient (typically the same
// provider as the working agent, but called separately and non-streamed).
type ModelJudge struct {
	client core.ModelClient
	model  string
}

// NewModelJudge returns a Judge backed by the given model client.
func NewModelJudge(client core.ModelClient, model string) *ModelJudge {
	return &ModelJudge{client: client, model: model}
}

// Evaluate calls the model with the judge system prompt and a rendered
// transcript, then parses a structured verdict. It returns an error when the
// model cannot be reached or the response cannot be parsed; callers treat such
// errors as fail-open (allow the agent to stop) so a flaky judge never traps
// the user.
func (j *ModelJudge) Evaluate(ctx context.Context, condition string, transcript []core.Message) (Verdict, error) {
	if j == nil || j.client == nil {
		return Verdict{}, fmt.Errorf("judge: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	req := core.ChatRequest{
		Model:       j.model,
		Temperature: 0,
		Messages: []core.Message{
			{Role: "system", Content: JudgeSystemPrompt},
			{Role: "user", Content: buildJudgeUserMessage(condition, transcript)},
		},
	}

	events, err := j.client.ChatStream(ctx, req)
	if err != nil {
		return Verdict{}, fmt.Errorf("judge: start stream: %w", err)
	}

	var b strings.Builder
	for {
		select {
		case <-ctx.Done():
			return Verdict{}, ctx.Err()
		case event, ok := <-events:
			if !ok {
				return parseVerdict(b.String())
			}
			if event.Err != nil {
				return Verdict{}, fmt.Errorf("judge: stream error: %w", event.Err)
			}
			b.WriteString(event.Delta)
			if event.Done {
				return parseVerdict(b.String())
			}
		}
	}
}

// buildJudgeUserMessage renders the condition and a compact transcript into a
// single user message for the judge.
func buildJudgeUserMessage(condition string, transcript []core.Message) string {
	var b strings.Builder
	b.WriteString("STOPPING CONDITION:\n")
	b.WriteString(strings.TrimSpace(condition))
	b.WriteString("\n\nTRANSCRIPT:\n")
	for _, msg := range transcript {
		role := strings.TrimSpace(msg.Role)
		if role == "system" {
			continue // the judge has its own system prompt; skip the agent's
		}
		text := strings.TrimSpace(msg.TextContent())
		if text == "" && len(msg.ToolCalls) == 0 {
			continue
		}
		if len(msg.ToolCalls) > 0 {
			names := make([]string, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				names = append(names, call.Name)
			}
			if text != "" {
				text += " "
			}
			text += "[tool_calls: " + strings.Join(names, ", ") + "]"
		}
		b.WriteString(fmt.Sprintf("%s: %s\n", role, truncateForJudge(text, 1200)))
	}
	return strings.TrimSpace(b.String())
}

func truncateForJudge(text string, limit int) string {
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "…[truncated]"
}

// parseVerdict extracts a JSON verdict from model output, tolerating
// surrounding prose or code fences.
func parseVerdict(text string) (Verdict, error) {
	raw := extractJSONObject(text)
	if raw == "" {
		return Verdict{}, fmt.Errorf("judge: no JSON object in response")
	}
	var v Verdict
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return Verdict{}, fmt.Errorf("judge: parse verdict: %w", err)
	}
	return v, nil
}

// extractJSONObject returns the substring between the first '{' and its matching
// '}' (by brace balance), or "" if none is found.
func extractJSONObject(text string) string {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return ""
}

// goalReminderMessage is the synthetic user turn injected when the judge says
// the goal is not yet met, telling the agent precisely what to keep working on.
func goalReminderMessage(condition, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "the condition is not yet verifiably satisfied."
	}
	return strings.Join([]string{
		"<system-reminder>",
		fmt.Sprintf("Your goal is not yet satisfied: %q.", strings.TrimSpace(condition)),
		"An independent judge reviewed the transcript and reported what is still missing:",
		reason,
		"Keep working toward the goal. Do not stop until it is genuinely met or proven impossible.",
		"</system-reminder>",
	}, "\n")
}

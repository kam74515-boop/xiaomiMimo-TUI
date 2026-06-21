package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mimo-tui/internal/core"
)

func TestExtractJSONObject(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`{"ok":true}`, `{"ok":true}`},
		{"prose before {\"ok\": false, \"reason\": \"x\"} and after", `{"ok": false, "reason": "x"}`},
		{"```json\n{\"ok\": true}\n```", `{"ok": true}`},
		{`{"reason":"has } brace in string","ok":true}`, `{"reason":"has } brace in string","ok":true}`},
		{"no json here", ""},
	}
	for _, c := range cases {
		if got := extractJSONObject(c.in); got != c.want {
			t.Errorf("extractJSONObject(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseVerdict(t *testing.T) {
	v, err := parseVerdict(`Sure: {"ok": false, "impossible": false, "reason": "tests missing"}`)
	if err != nil {
		t.Fatalf("parseVerdict error: %v", err)
	}
	if v.OK || v.Impossible || v.Reason != "tests missing" {
		t.Fatalf("unexpected verdict: %+v", v)
	}
	if _, err := parseVerdict("not json"); err == nil {
		t.Fatal("expected error for non-JSON verdict")
	}
}

// fakeJudge returns scripted verdicts (or an error) in order.
type fakeJudge struct {
	verdicts []Verdict
	err      error
	calls    int
}

func (j *fakeJudge) Evaluate(ctx context.Context, condition string, transcript []core.Message) (Verdict, error) {
	j.calls++
	if j.err != nil {
		return Verdict{}, j.err
	}
	if len(j.verdicts) == 0 {
		return Verdict{OK: true}, nil
	}
	v := j.verdicts[0]
	j.verdicts = j.verdicts[1:]
	return v, nil
}

// finalAnswerClient always returns a final answer (no tool calls).
func finalAnswerClient() *fakeClient {
	return &fakeClient{
		streamFn: func(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error) {
			ch := make(chan core.ModelEvent, 2)
			ch <- core.ModelEvent{Delta: "final answer"}
			ch <- core.ModelEvent{Done: true}
			close(ch)
			return ch, nil
		},
	}
}

func lastGoalUpdate(events []core.AgentEvent) *core.GoalUpdate {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == core.EventGoalUpdate && events[i].Goal != nil {
			return events[i].Goal
		}
	}
	return nil
}

func TestGoalGateReentriesUntilSatisfied(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(256)

	judge := &fakeJudge{verdicts: []Verdict{
		{OK: false, Reason: "no tests yet"},
		{OK: false, Reason: "tests still failing"},
		{OK: true, Reason: "tests pass"},
	}}
	config := LoopConfig{
		MaxSteps:      10,
		StepTimeout:   10 * time.Second,
		TotalTimeout:  30 * time.Second,
		GoalCondition: "add passing tests",
		Judge:         judge,
		MaxGoalReact:  12,
	}

	_, err := Loop(context.Background(), "go", finalAnswerClient(), &fakeExecutor{}, &fakeContextManager{}, nil, bus, config, nil)
	if err != nil {
		t.Fatalf("Loop returned error: %v", err)
	}
	if judge.calls != 3 {
		t.Fatalf("judge called %d times, want 3", judge.calls)
	}
	events := drainBus(sub)
	final := lastGoalUpdate(events)
	if final == nil || !final.Satisfied {
		t.Fatalf("final goal update = %+v, want satisfied", final)
	}
	// At least one re-entry should have been an active (pending) update.
	sawActive := false
	for _, e := range events {
		if e.Type == core.EventGoalUpdate && e.Goal != nil && e.Goal.Active {
			sawActive = true
		}
	}
	if !sawActive {
		t.Fatal("expected at least one active goal update during re-entry")
	}
}

func TestGoalGateGivesUpAtCap(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(256)

	judge := &fakeJudge{verdicts: []Verdict{
		{OK: false, Reason: "nope"},
		{OK: false, Reason: "nope"},
		{OK: false, Reason: "nope"},
	}}
	config := LoopConfig{
		MaxSteps:      10,
		StepTimeout:   10 * time.Second,
		TotalTimeout:  30 * time.Second,
		GoalCondition: "impossible-ish",
		Judge:         judge,
		MaxGoalReact:  2,
	}

	_, err := Loop(context.Background(), "go", finalAnswerClient(), &fakeExecutor{}, &fakeContextManager{}, nil, bus, config, nil)
	if err != nil {
		t.Fatalf("Loop returned error: %v", err)
	}
	// react 1, react 2, then cap reached on the third evaluation.
	if judge.calls != 3 {
		t.Fatalf("judge called %d times, want 3", judge.calls)
	}
	final := lastGoalUpdate(drainBus(sub))
	if final == nil || final.Active || final.Satisfied {
		t.Fatalf("final goal update = %+v, want inactive & not satisfied", final)
	}
}

func TestGoalGateFailOpenOnJudgeError(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(256)

	client := finalAnswerClient()
	judge := &fakeJudge{err: errors.New("provider down")}
	config := LoopConfig{
		MaxSteps:      10,
		StepTimeout:   10 * time.Second,
		TotalTimeout:  30 * time.Second,
		GoalCondition: "do something",
		Judge:         judge,
		MaxGoalReact:  12,
	}

	_, err := Loop(context.Background(), "go", client, &fakeExecutor{}, &fakeContextManager{}, nil, bus, config, nil)
	if err != nil {
		t.Fatalf("Loop returned error: %v", err)
	}
	if judge.calls != 1 {
		t.Fatalf("judge called %d times, want 1 (fail-open, no re-entry)", judge.calls)
	}
	final := lastGoalUpdate(drainBus(sub))
	if final == nil || final.Active {
		t.Fatalf("final goal update = %+v, want inactive (fail-open allows stop)", final)
	}
	if !strings.Contains(final.Reason, "fail-open") {
		t.Fatalf("reason = %q, want fail-open explanation", final.Reason)
	}
}

func TestLoopNoGoalIsUnchanged(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(64)
	// No goal/judge configured: loop stops at the first final answer.
	_, err := Loop(context.Background(), "hi", finalAnswerClient(), &fakeExecutor{}, &fakeContextManager{}, nil, bus, DefaultLoopConfig(), nil)
	if err != nil {
		t.Fatalf("Loop returned error: %v", err)
	}
	if lastGoalUpdate(drainBus(sub)) != nil {
		t.Fatal("did not expect any goal update without an active goal")
	}
}

// toolCallClient always asks for a tool call, so the loop never reaches the
// goal gate and instead exhausts MaxSteps.
func toolCallClient() *fakeClient {
	return &fakeClient{
		streamFn: func(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error) {
			ch := make(chan core.ModelEvent, 2)
			ch <- core.ModelEvent{ToolCalls: []core.ToolCall{{ID: "c", Name: "list_dir"}}}
			ch <- core.ModelEvent{Done: true}
			close(ch)
			return ch, nil
		},
	}
}

func TestGoalClosedOnMaxSteps(t *testing.T) {
	bus := core.NewBus()
	sub := bus.Subscribe(256)
	config := LoopConfig{
		MaxSteps:      2,
		StepTimeout:   10 * time.Second,
		TotalTimeout:  30 * time.Second,
		GoalCondition: "never reached because tools keep running",
		Judge:         &fakeJudge{verdicts: []Verdict{{OK: true}}},
		MaxGoalReact:  12,
	}
	_, err := Loop(context.Background(), "go", toolCallClient(), &fakeExecutor{}, &fakeContextManager{}, nil, bus, config, nil)
	if err == nil || !strings.Contains(err.Error(), "max steps") {
		t.Fatalf("err = %v, want max steps error", err)
	}
	final := lastGoalUpdate(drainBus(sub))
	if final == nil || final.Active {
		t.Fatalf("expected a terminal goal update on max steps, got %+v", final)
	}
}

// cancelJudge cancels the run during evaluation, simulating an interrupt that
// lands while the judge is being consulted.
type cancelJudge struct{ cancel context.CancelFunc }

func (j *cancelJudge) Evaluate(ctx context.Context, condition string, transcript []core.Message) (Verdict, error) {
	j.cancel()
	return Verdict{}, context.Canceled
}

func TestGoalGateCancellationPropagates(t *testing.T) {
	bus := core.NewBus()
	bus.Subscribe(64)
	ctx, cancel := context.WithCancel(context.Background())
	config := LoopConfig{
		MaxSteps:      10,
		StepTimeout:   10 * time.Second,
		TotalTimeout:  30 * time.Second,
		GoalCondition: "do x",
		Judge:         &cancelJudge{cancel: cancel},
		MaxGoalReact:  12,
	}
	_, err := Loop(ctx, "go", finalAnswerClient(), &fakeExecutor{}, &fakeContextManager{}, nil, bus, config, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled (cancellation must not be masked as a clean stop)", err)
	}
}

func TestModelJudgeParsesProviderVerdict(t *testing.T) {
	client := &fakeClient{
		streamFn: func(ctx context.Context, req core.ChatRequest) (<-chan core.ModelEvent, error) {
			ch := make(chan core.ModelEvent, 3)
			ch <- core.ModelEvent{Delta: `{"ok": true, `}
			ch <- core.ModelEvent{Delta: `"impossible": false, "reason": "looks done"}`}
			ch <- core.ModelEvent{Done: true}
			close(ch)
			return ch, nil
		},
	}
	j := NewModelJudge(client, "mimo-test")
	v, err := j.Evaluate(context.Background(), "ship it", []core.Message{{Role: "assistant", Content: "did the thing"}})
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if !v.OK || v.Reason != "looks done" {
		t.Fatalf("verdict = %+v, want ok with reason", v)
	}
	// The judge must not request streaming tool execution; it sends temperature 0.
	if client.req.Temperature != 0 {
		t.Fatalf("judge temperature = %v, want 0", client.req.Temperature)
	}
}

package benchmark

import (
	"strings"
	"testing"

	"mimo-tui/internal/core"
	"mimo-tui/internal/session"
)

func TestGenerateResumeEvents(t *testing.T) {
	tests := []struct {
		name  string
		count int
	}{
		{"small", 10},
		{"medium", 100},
		{"large", 1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := GenerateResumeEvents(tt.count)
			if len(events) != tt.count {
				t.Fatalf("generated %d events, want %d", len(events), tt.count)
			}

			// Verify events have valid types.
			validTypes := map[core.EventType]bool{
				core.EventUserPrompt:    true,
				core.EventAgentStarted:  true,
				core.EventMessageDelta:  true,
				core.EventToolStart:     true,
				core.EventToolResult:    true,
				core.EventObservation:   true,
				core.EventContextUpdate: true,
				core.EventTraceUpdate:   true,
				core.EventDone:          true,
			}
			for i, e := range events {
				if !validTypes[e.Type] {
					t.Fatalf("event[%d] has invalid type %q", i, e.Type)
				}
				if e.Time.IsZero() {
					t.Fatalf("event[%d] has zero time", i)
				}
			}

			// Verify chronological ordering.
			for i := 1; i < len(events); i++ {
				if events[i].Time.Before(events[i-1].Time) {
					t.Fatalf("event[%d] time %v is before event[%d] time %v",
						i, events[i].Time, i-1, events[i-1].Time)
				}
			}
		})
	}
}

func TestRunResumeBenchmark(t *testing.T) {
	result := RunResumeBenchmark(50)

	if result.EventCount != 50 {
		t.Fatalf("EventCount = %d, want 50", result.EventCount)
	}
	if result.ExtractHistoryNs <= 0 {
		t.Fatal("ExtractHistoryNs should be positive")
	}
	if result.BuildSummaryNs <= 0 {
		t.Fatal("BuildSummaryNs should be positive")
	}
	if result.MessageCount < 2 {
		t.Fatalf("MessageCount = %d, want >= 2", result.MessageCount)
	}
	if result.ArtifactCount < 1 {
		t.Fatalf("ArtifactCount = %d, want >= 1", result.ArtifactCount)
	}
}

func TestRunResumeBenchmarkScales(t *testing.T) {
	small := RunResumeBenchmark(20)
	large := RunResumeBenchmark(200)

	// Larger event counts should produce more messages and artifacts.
	if large.MessageCount <= small.MessageCount {
		t.Fatalf("large message count (%d) should exceed small (%d)",
			large.MessageCount, small.MessageCount)
	}
	if large.ArtifactCount <= small.ArtifactCount {
		t.Fatalf("large artifact count (%d) should exceed small (%d)",
			large.ArtifactCount, small.ArtifactCount)
	}
}

func TestFormatResumeReport(t *testing.T) {
	results := []ResumeBenchmarkResult{
		{EventCount: 10, ExtractHistoryNs: 1000000, BuildSummaryNs: 2000000, MessageCount: 5, ArtifactCount: 1, TraceCount: 1},
		{EventCount: 100, ExtractHistoryNs: 10000000, BuildSummaryNs: 20000000, MessageCount: 50, ArtifactCount: 10, TraceCount: 5},
	}

	report := FormatResumeReport(results)

	if !strings.Contains(report, "# Resume Benchmark Results") {
		t.Fatal("report missing header")
	}
	if !strings.Contains(report, "ExtractHistory") {
		t.Fatal("report missing ExtractHistory column")
	}
	if !strings.Contains(report, "BuildSummary") {
		t.Fatal("report missing BuildSummary column")
	}
	if !strings.Contains(report, "| 10 |") {
		t.Fatal("report missing first row event count")
	}
	if !strings.Contains(report, "| 100 |") {
		t.Fatal("report missing second row event count")
	}
}

func TestFormatResumeReportEmpty(t *testing.T) {
	report := FormatResumeReport(nil)
	if !strings.Contains(report, "# Resume Benchmark Results") {
		t.Fatal("empty report should still have header")
	}
}

func BenchmarkExtractHistory(b *testing.B) {
	events := GenerateResumeEvents(500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = session.ExtractHistory(events)
	}
}

func BenchmarkBuildResumeSummary(b *testing.B) {
	events := GenerateResumeEvents(500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = session.BuildResumeSummary(events)
	}
}

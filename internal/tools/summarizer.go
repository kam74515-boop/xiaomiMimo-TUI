package tools

import (
	"mimo-tui/internal/core"
)

// BudgetLevel classifies how aggressive the summarizer should be when
// reducing tool output to an observation.
type BudgetLevel int

const (
	BudgetSafe     BudgetLevel = iota // Keep the most detail; safe for normal context budgets.
	BudgetWarning                     // Moderate truncation; budget is filling up.
	BudgetCritical                    // Severe truncation; only essential signals survive.
)

// Summarizer converts a raw ToolResult into a budget-aware Observation.
// Implementations are tool-specific and must honour the BudgetLevel to
// produce progressively terser output.
type Summarizer interface {
	Summarize(result core.ToolResult, budget BudgetLevel) core.Observation
}

// BudgetFromContext computes a BudgetLevel based on how much of the context
// window has been consumed. The calculation is deliberately simple:
//
//	< 50% budget used → BudgetSafe
//	50-80% budget used → BudgetWarning
//	> 80% budget used → BudgetCritical
func BudgetFromContext(windowTokens, usedTokens int) BudgetLevel {
	if windowTokens <= 0 {
		return BudgetSafe
	}
	ratio := float64(usedTokens) / float64(windowTokens)
	switch {
	case ratio >= 0.8:
		return BudgetCritical
	case ratio >= 0.5:
		return BudgetWarning
	default:
		return BudgetSafe
	}
}

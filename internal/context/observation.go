package context

import (
	"strings"

	"mimo-tui/internal/core"
)

const (
	ObservationSource = "observation"
	ArtifactSource    = "artifact:"
)

func PromoteObservation(id string, obs core.Observation) core.ContextItem {
	tier := observationTier(obs.ContextPlacement)

	source := ObservationSource
	if obs.ArtifactID != "" {
		source = ArtifactSource + obs.ArtifactID
	}

	return core.ContextItem{
		ID:            strings.TrimSpace(id),
		Tier:          tier,
		Title:         observationTitle(obs),
		Source:        source,
		TokenEstimate: EstimateTokens(observationText(obs)),
		Reason:        observationReason(tier, obs.ArtifactID),
	}
}

func observationTier(tier core.ContextTier) core.ContextTier {
	switch tier {
	case core.TierNear, core.TierAnchor, core.TierArtifact:
		return tier
	default:
		return core.TierNear
	}
}

func observationTitle(obs core.Observation) string {
	for _, candidate := range []string{obs.Summary, obs.StateDelta, obs.RiskDelta} {
		if text := strings.TrimSpace(candidate); text != "" {
			return text
		}
	}
	return "Observation"
}

func observationText(obs core.Observation) string {
	parts := []string{
		strings.TrimSpace(obs.Summary),
		strings.TrimSpace(obs.StateDelta),
		strings.TrimSpace(obs.RiskDelta),
		strings.TrimSpace(strings.Join(obs.NextAffordances, "\n")),
	}

	var nonEmpty []string
	for _, part := range parts {
		if part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, "\n")
}

func observationReason(tier core.ContextTier, artifactID string) string {
	reason := "Promoted observation to " + string(tier) + " context"
	if artifactID != "" {
		reason += " from artifact " + artifactID
	}
	return reason
}

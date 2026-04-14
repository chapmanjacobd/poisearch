package search

import (
	// "math" // Reserved for future use

	"github.com/blevesearch/bleve/v2/search"
)

// ScorePenalty represents a penalty/bonus to apply to a search result's score.
// Inspired by Nominatim's penalty-based ranking system.
type ScorePenalty struct {
	// Description is a human-readable description of the penalty
	Description string
	// Penalty value to add to the result's score (positive = worse, negative = better)
	Penalty float64
}

// PenaltyType defines the type of penalty to apply.
type PenaltyType int

const (
	PenaltyPartialMatch PenaltyType = iota
	PenaltyWordBreak
	PenaltyDirection
	PenaltyProximity
	PenaltyImportance
)

// RankingConfig configures the penalty-based ranking system.
type RankingConfig struct {
	// EnablePenalties turns on penalty-based ranking when true.
	EnablePenalties bool `toml:"enable_penalties"`

	// ProximityWeight controls how much distance affects ranking.
	// Higher values mean closer results are ranked higher.
	// 0.0 = distance ignored, 1.0 = strong distance influence
	ProximityWeight float64 `toml:"proximity_weight"`

	// PartialMatchPenalty is the penalty applied for partial/prefix matches.
	// Higher values penalize partial matches more.
	PartialMatchPenalty float64 `toml:"partial_match_penalty"`

	// WordBreakPenalty is applied when query has word breaks.
	// Higher values penalize multi-word queries more.
	WordBreakPenalty float64 `toml:"word_break_penalty"`
}

// DefaultRankingConfig returns sensible defaults for the ranking system.
func DefaultRankingConfig() RankingConfig {
	return RankingConfig{
		EnablePenalties:     true,
		ProximityWeight:     0.5,
		PartialMatchPenalty: 0.3,
		WordBreakPenalty:    0.1,
	}
}

/*
// applyProximityPenalty adjusts the score based on distance from query location.
// Returns the adjusted score.
func applyProximityPenalty(score, distMeters, weight float64) float64 {
	if weight <= 0 || distMeters <= 0 {
		return score
	}

	// Exponential decay: closer results get less penalty
	// penalty = weight * (1 - e^(-dist/scale))
	// where scale is chosen so that at 1km, penalty is ~50% of max
	const scale = 1442.7 // ln(2) * 2000, so 1km gives 0.5 penalty factor

	penalty := weight * (1.0 - math.Exp(-distMeters/scale))
	return score * (1.0 - penalty)
}

// applyPartialMatchPenalty penalizes results that matched via prefix/partial.
func applyPartialMatchPenalty(score float64, isPrefix bool, penalty float64) float64 {
	if !isPrefix || penalty <= 0 {
		return score
	}
	return score * (1.0 - penalty)
}

// applyWordBreakPenalty penalizes results for multi-word queries.
func applyWordBreakPenalty(score float64, wordCount int, penalty float64) float64 {
	if wordCount <= 1 || penalty <= 0 {
		return score
	}
	// Each additional word adds the penalty
	return score * (1.0 - penalty*float64(wordCount-1))
}

// applyImportanceBoost boosts results with higher importance scores.
func applyImportanceBoost(score, importance float64) float64 {
	if importance <= 0 {
		return score
	}
	// Logarithmic boost: importance has diminishing returns
	return score + math.Log1p(importance)*0.5
}

// adjustScore applies all configured penalties/boosts to a result's score.
func adjustScore(score float64, hit *search.DocumentMatch, params SearchParams, ranking RankingConfig) float64 {
	if !ranking.EnablePenalties {
		return score
	}

	// Apply proximity penalty if coordinates and radius are provided
	if params.Lat != nil && params.Lon != nil && params.Radius != "" {
		// Extract distance from hit if available
		distMeters := extractDistanceFromHit(hit)
		if distMeters > 0 {
			score = applyProximityPenalty(score, distMeters, ranking.ProximityWeight)
		}
	}

	// Apply partial match penalty
	isPrefix := params.Prefix
	score = applyPartialMatchPenalty(score, isPrefix, ranking.PartialMatchPenalty)

	// Apply word break penalty
	wordCount := params.QueryFields()
	score = applyWordBreakPenalty(score, wordCount, ranking.WordBreakPenalty)

	// Apply importance boost
	importance := extractImportanceFromHit(hit)
	score = applyImportanceBoost(score, importance)

	return score
}
*/

// extractDistanceFromHit extracts the distance from a search hit.
func extractDistanceFromHit(hit *search.DocumentMatch) float64 {
	if hit == nil {
		return 0
	}

	// Try to get distance from fields
	if fields, ok := hit.Fields["_geo_distance"]; ok {
		if v, ok := fields.(float64); ok {
			return v
		}
	}

	return 0
}

// extractImportanceFromHit extracts the importance from a search hit.
func extractImportanceFromHit(hit *search.DocumentMatch) float64 {
	if hit == nil {
		return 0
	}

	if fields, ok := hit.Fields["importance"]; ok {
		if v, ok := fields.(float64); ok {
			return v
		}
	}

	return 0
}

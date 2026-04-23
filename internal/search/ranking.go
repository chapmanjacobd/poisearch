package search

import (
	"math"
	"strings"
	"unicode"
)

// IsPlaceIntentQuery reports whether the query looks like a raw place-name lookup
// rather than a category, address, or metadata search.
func IsPlaceIntentQuery(params SearchParams) bool {
	if strings.TrimSpace(params.Query) == "" || IsNearQuery(params.Query) {
		return false
	}
	if params.QueryFields() > 3 {
		return false
	}
	if params.Key != "" || params.Value != "" || len(params.Keys) > 0 || len(params.Values) > 0 {
		return false
	}
	if params.Street != "" || params.HouseNumber != "" || params.Postcode != "" ||
		params.City != "" || params.Country != "" || params.Floor != "" ||
		params.Unit != "" || params.Level != "" {

		return false
	}
	if params.Phone != "" || params.Wheelchair != "" || params.OpeningHours != "" {
		return false
	}
	return true
}

// IsPlaceLikeClassification reports whether a result represents a place/settlement-like feature.
func IsPlaceLikeClassification(key, value string) bool {
	if key != "place" {
		return false
	}
	switch value {
	case "city", "town", "village", "hamlet", "suburb", "quarter", "neighbourhood", "locality",
		"municipality", "county", "state", "province", "region", "country", "island", "islet":
		return true
	default:
		return true
	}
}

type EntityTier int

const (
	EntityTierSecondary EntityTier = iota
	EntityTierPrimary
	EntityTierPlace
)

type RankingSignals struct {
	BaseScore          float64
	NameMatchScore     float64
	CategoryMatchScore float64
	EntityTier         EntityTier
	Importance         float64
	ExactNameMatch     bool
	HasName            bool
}

// EntityTierForClassification maps a classification to a broad ranking tier.
func EntityTierForClassification(key, value string) EntityTier {
	if IsPlaceLikeClassification(key, value) {
		return EntityTierPlace
	}

	switch key {
	case "amenity", "shop", "tourism", "leisure", "historic", "natural",
		"healthcare", "public_transport", "railway", "aeroway", "office":
		return EntityTierPrimary
	default:
		return EntityTierSecondary
	}
}

// SharedRankingScore applies one ranking policy across indexed and direct modes.
func SharedRankingScore(params SearchParams, signals RankingSignals) float64 {
	if strings.TrimSpace(params.Query) == "" {
		return signals.BaseScore + max(signals.Importance, 0)*1000
	}

	score := signals.BaseScore
	score += signals.NameMatchScore * 1000
	score += signals.CategoryMatchScore * 1000

	if signals.ExactNameMatch {
		score += 160_000
	}

	switch signals.EntityTier {
	case EntityTierPlace:
		if signals.ExactNameMatch || signals.NameMatchScore >= 300 {
			score += 120_000
		}
	case EntityTierPrimary:
		if signals.CategoryMatchScore > 0 {
			score += 140_000
		} else if signals.NameMatchScore > 0 {
			score += 70_000
		}
	case EntityTierSecondary:
		if signals.CategoryMatchScore > 0 {
			score += 60_000
		} else if signals.NameMatchScore > 0 {
			score += 15_000
		}
	}

	if !signals.HasName {
		score -= 160_000
	}

	score += importanceTieBreak(signals.Importance)
	return score
}

func importanceTieBreak(importance float64) float64 {
	if importance <= 0 {
		return 0
	}
	return math.Log1p(importance) * 1000
}

func BestTextMatchScore(values []string, query string) float64 {
	best := 0.0
	for _, value := range values {
		best = max(best, TextMatchScore(value, query))
	}
	return best
}

func TextMatchScore(value, query string) float64 {
	if value == "" || strings.TrimSpace(query) == "" {
		return 0
	}

	valueLower := normalizeQuery(strings.TrimSpace(value))
	queryLower := normalizeQuery(strings.TrimSpace(query))

	switch {
	case directTextTokenMatch(valueLower, queryLower):
		return 400
	case strings.Contains(valueLower, queryLower):
		return 300
	case directTextPrefixMatch(valueLower, queryLower):
		return 220
	case directTextFuzzyMatch(valueLower, queryLower):
		return 160
	default:
		return 0
	}
}

func ExactNormalizedMatch(values []string, query string) bool {
	normalizedQuery := normalizeQuery(strings.TrimSpace(query))
	if normalizedQuery == "" {
		return false
	}

	for _, value := range values {
		if normalizeQuery(strings.TrimSpace(value)) == normalizedQuery {
			return true
		}
	}
	return false
}

func CategoryMatchScore(query, key, value string) float64 {
	query = normalizeQuery(strings.TrimSpace(query))
	if query == "" {
		return 0
	}
	if !isCategorySignalKey(key) {
		return 0
	}

	score := 0.0
	switch {
	case directTextTokenMatch(normalizeQuery(value), query):
		score = max(score, 320)
	case directTextTokenMatch(normalizeQuery(key), query):
		score = max(score, 140)
	}

	for _, match := range resolveCategoryMatches(query) {
		if match.Key == key && match.Value == value {
			score = max(score, 300)
		}
	}

	return score
}

func isCategorySignalKey(key string) bool {
	if strings.HasPrefix(key, "addr:") {
		return false
	}

	switch key {
	case "brand", "operator",
		"ref", "int_ref", "nat_ref", "reg_ref",
		"official_name", "loc_name", "reg_name",
		"wheelchair", "internet_access":
		return false
	default:
		return true
	}
}

func CollectNameValues(fields map[string]any, langs []string) []string {
	values := make([]string, 0, 6+len(langs))
	for _, field := range nameFieldNames(langs) {
		if value, ok := fields[field].(string); ok && value != "" {
			values = append(values, value)
		}
	}
	return values
}

func CollectTagNameValues(tags map[string]string, langs []string) []string {
	values := make([]string, 0, 6+len(langs))
	for _, field := range nameFieldNames(langs) {
		if value := tags[field]; value != "" {
			values = append(values, value)
		}
	}
	return values
}

func nameFieldNames(langs []string) []string {
	fields := make([]string, 0, 6+len(langs))
	fields = append(fields, "name", "alt_name", "old_name", "short_name", "brand", "operator")
	for _, lang := range langs {
		fields = append(fields, "name:"+lang)
	}
	return fields
}

func directTextTokenMatch(value, query string) bool {
	return strings.TrimSpace(value) == strings.TrimSpace(query)
}

func directTextPrefixMatch(value, query string) bool {
	compactQuery := compactText(query)
	if compactQuery == "" {
		return false
	}

	if strings.HasPrefix(compactText(value), compactQuery) {
		return true
	}

	for _, token := range tokenizeText(value) {
		if strings.HasPrefix(token, compactQuery) {
			return true
		}
	}

	return false
}

func directTextFuzzyMatch(value, query string) bool {
	compactQuery := compactText(query)
	if compactQuery == "" {
		return false
	}

	if boundedEditDistance(compactText(value), compactQuery, 1) <= 1 {
		return true
	}

	for _, token := range tokenizeText(value) {
		if boundedEditDistance(token, compactQuery, 1) <= 1 {
			return true
		}
	}

	return false
}

func compactText(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range normalizeQuery(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func tokenizeText(value string) []string {
	return strings.FieldsFunc(normalizeQuery(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func boundedEditDistance(a, b string, maxDistance int) int {
	if intAbs(len(a)-len(b)) > maxDistance {
		return maxDistance + 1
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		minInRow := curr[0]
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}

			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
			if curr[j] < minInRow {
				minInRow = curr[j]
			}
		}

		if minInRow > maxDistance {
			return maxDistance + 1
		}

		prev, curr = curr, prev
	}

	if prev[len(b)] > maxDistance {
		return maxDistance + 1
	}

	return prev[len(b)]
}

func intAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

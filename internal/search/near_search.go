package search

import (
	"regexp"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

// nearOperators are the keywords that trigger NearSearch mode.
// These match Nominatim's pattern for "category near place" queries.
var nearOperators = []string{"near", "in", "around", "close to"}

// nearPattern matches "X near Y" or "X in Y" patterns.
// Examples: "restaurants near Big Ben", "hotels in Berlin"
var nearPattern = regexp.MustCompile(`(?i)^(.+?)\s+(near|in|around|close\s+to)\s+(.+)$`)

// NearResult represents the result of a NearSearch query.
type NearResult struct {
	// Category is the searched category (e.g., "restaurants")
	Category string
	// ReferencePlace is the resolved reference location (e.g., "Big Ben")
	ReferencePlace string
	// Lat/Lon are the coordinates of the resolved reference place
	Lat, Lon float64
	// Results are the POIs found near the reference place
	Results *bleve.SearchResult
}

// parseNearQuery checks if the query matches a "X near Y" pattern.
// Returns (category, referencePlace, operator, true) if matched, else (..., false).
func parseNearQuery(q string) (category, referencePlace, operator string, isNear bool) {
	matches := nearPattern.FindStringSubmatch(q)
	if matches == nil {
		return "", "", "", false
	}

	category = strings.TrimSpace(matches[1])
	operator = strings.ToLower(strings.TrimSpace(matches[2]))
	referencePlace = strings.TrimSpace(matches[3])

	// Validate: category should be a known POI type or short phrase
	// (typically 1-3 words, not a full sentence)
	if len(strings.Fields(category)) > 5 {
		return "", "", "", false
	}

	return category, referencePlace, operator, true
}

// isNearQuery returns true if the query looks like a "X near Y" pattern.
func isNearQuery(q string) bool {
	_, _, _, ok := parseNearQuery(q)
	return ok
}

// NearSearch executes a "X near Y" query:
// 1. Searches for the reference place Y to get coordinates
// 2. Searches for category X near those coordinates
//
// This is the pattern from Nominatim's NearSearch that enables
// queries like "restaurants near Big Ben" or "hotels in Berlin".
func NearSearch(index bleve.Index, baseParams SearchParams, category, referencePlace string) (*NearResult, error) {
	// Phase 1: Find the reference place
	refParams := SearchParams{
		Query:    referencePlace,
		Limit:    1,
		GeoMode:  baseParams.GeoMode,
		Langs:    baseParams.Langs,
		Analyzer: baseParams.Analyzer,
	}

	refResults, err := Search(index, refParams)
	if err != nil {
		return nil, err
	}

	if refResults == nil || len(refResults.Hits) == 0 {
		// Reference place not found, return empty results
		return &NearResult{
			Category:       category,
			ReferencePlace: referencePlace,
			Results:        &bleve.SearchResult{},
		}, nil
	}

	// Extract coordinates from first hit
	hit := refResults.Hits[0]
	var lat, lon float64

	// Try to get coordinates from the hit
	if fields, ok := hit.Fields["lat"]; ok {
		switch v := fields.(type) {
		case float64:
			lat = v
		}
	}
	if fields, ok := hit.Fields["lon"]; ok {
		switch v := fields.(type) {
		case float64:
			lon = v
		}
	}

	// If no coordinates found, return empty results
	if lat == 0 && lon == 0 {
		return &NearResult{
			Category:       category,
			ReferencePlace: referencePlace,
			Lat:            lat,
			Lon:            lon,
			Results:        &bleve.SearchResult{},
		}, nil
	}

	// Phase 2: Search for category near the reference location
	searchParams := SearchParams{
		Query:    category,
		Lat:      &lat,
		Lon:      &lon,
		Radius:   "5000m", // Default 5km radius for near search
		Limit:    baseParams.Limit,
		GeoMode:  baseParams.GeoMode,
		Langs:    baseParams.Langs,
		Analyzer: baseParams.Analyzer,
		Class:    baseParams.Class,   // Preserve class filter if set
		Subtype:  baseParams.Subtype, // Preserve subtype filter if set
		Classes:  baseParams.Classes,
		Subtypes: baseParams.Subtypes,
	}

	results, err := Search(index, searchParams)
	if err != nil {
		return nil, err
	}

	return &NearResult{
		Category:       category,
		ReferencePlace: referencePlace,
		Lat:            lat,
		Lon:            lon,
		Results:        results,
	}, nil
}

// buildNearQuery creates a combined query for "X near Y" pattern.
// This is used when we want to search both in a single Bleve query
// rather than two-phase search. Returns nil if not a near query.
func buildNearQuery(q string, params SearchParams) query.Query {
	category, referencePlace, _, isNear := parseNearQuery(q)
	if !isNear {
		return nil
	}

	// Create a disjunction that matches either:
	// 1. The category terms
	// 2. The reference place terms
	// With higher boost for category matches

	categoryQuery := bleve.NewMatchQuery(category)
	categoryQuery.SetBoost(2.0)

	refQuery := bleve.NewMatchQuery(referencePlace)
	refQuery.SetBoost(0.5) // Lower boost for reference place

	return bleve.NewDisjunctionQuery(categoryQuery, refQuery)
}

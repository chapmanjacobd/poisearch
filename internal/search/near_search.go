package search

import (
	"regexp"
	"strings"

	"github.com/blevesearch/bleve/v2"
	blevesearch "github.com/blevesearch/bleve/v2/search"
)

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
// Returns (category, referencePlace, true) if matched, else (..., false).
func parseNearQuery(q string) (category, referencePlace string, isNear bool) {
	matches := nearPattern.FindStringSubmatch(q)
	if matches == nil {
		return "", "", false
	}

	category = strings.TrimSpace(matches[1])
	referencePlace = strings.TrimSpace(matches[3])

	// Validate: category should be a known POI type or short phrase
	// (typically 1-3 words, not a full sentence)
	if len(strings.Fields(category)) > 5 {
		return "", "", false
	}

	return category, referencePlace, true
}

// isNearQuery returns true if the query looks like a "X near Y" pattern.
func isNearQuery(q string) bool {
	_, _, ok := parseNearQuery(q)
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
	lat, lon, ok := hitLatLon(hit)

	// If no coordinates found, return empty results
	if !ok {
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
		Key:      baseParams.Key,   // Preserve key filter if set
		Value:    baseParams.Value, // Preserve value filter if set
		Keys:     baseParams.Keys,
		Values:   baseParams.Values,
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

func hitLatLon(hit *blevesearch.DocumentMatch) (lat, lon float64, ok bool) {
	geometry, ok := hit.Fields["geometry"]
	if !ok {
		return 0, 0, false
	}

	switch geom := geometry.(type) {
	case []float64:
		if len(geom) == 2 {
			return geom[1], geom[0], true
		}
	case []any:
		if len(geom) == 2 {
			lon, lonOK := geom[0].(float64)
			lat, latOK := geom[1].(float64)
			if lonOK && latOK {
				return lat, lon, true
			}
		}
	case map[string]any:
		if lat, latOK := geom["lat"].(float64); latOK {
			if lon, lonOK := geom["lon"].(float64); lonOK {
				return lat, lon, true
			}
		}
		if coords, ok := geom["coordinates"].([]any); ok && len(coords) == 2 {
			lon, lonOK := coords[0].(float64)
			lat, latOK := coords[1].(float64)
			if lonOK && latOK {
				return lat, lon, true
			}
		}
	case map[string]float64:
		lat, latOK := geom["lat"]
		lon, lonOK := geom["lon"]
		if latOK && lonOK {
			return lat, lon, true
		}
	}

	return 0, 0, false
}

package search

import (
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

type Boostable interface {
	SetBoost(float64)
}

// QueryInterpretation represents one way to parse a multi-word query.
type QueryInterpretation struct {
	// Description explains how this interpretation splits the query
	Description string
	// Queries are the Bleve queries for this interpretation
	Queries []query.Query
	// Penalty is the interpretation penalty (lower = better)
	Penalty float64
}

// CategoryMatch represents a possible OSM key/value pair for a category name.
type CategoryMatch struct {
	Key   string
	Value string
}

// CategoryMapper is a global hook to allow resolving category names (e.g., "restaurants")
// to OSM key/value pairs. Set this at startup from the osm package.
//
//nolint:gochecknoglobals // Hook for ontology resolution without circular dependencies
var CategoryMapper func(query string) []CategoryMatch

// generateInterpretations creates multiple interpretations of a multi-word query.
// Inspired by Nominatim's query DAG approach where "Berlin Mitte" could be:
// 1. [name="Berlin Mitte"] (full phrase)
// 2. [name="Berlin", name="Mitte"] (separate terms)
// 3. [name="Berlin", address="Mitte"] (city + district)
func generateInterpretations(q string) []QueryInterpretation {
	terms := strings.Fields(q)
	if len(terms) == 0 {
		return nil
	}

	interpretations := make([]QueryInterpretation, 0, 5)

	// Interpretation 1: Full phrase match (highest quality, lowest penalty)
	fullQuery := addNameQuery(q, "name")
	interpretations = append(interpretations, QueryInterpretation{
		Description: "full phrase",
		Queries:     []query.Query{fullQuery},
		Penalty:     0.0,
	})

	// Interpretation 2: Check for category match (e.g. "restaurants")
	if CategoryMapper != nil {
		matches := CategoryMapper(q)
		if len(matches) > 0 {
			catQueries := make([]query.Query, 0, len(matches)*2)
			for _, m := range matches {
				cq1 := bleve.NewMatchQuery(m.Value)
				cq1.SetField("value")
				cq2 := bleve.NewMatchQuery(m.Value)
				cq2.SetField("values")
				catQueries = append(catQueries, cq1, cq2)
			}
			interpretations = append(interpretations, QueryInterpretation{
				Description: "category match",
				Queries:     []query.Query{bleve.NewDisjunctionQuery(catQueries...)},
				Penalty:     0.0, // Categories are high-intent
			})
		}
	}

	if len(terms) <= 1 {
		return interpretations
	}

	// Interpretation 3: All terms as separate conjuncts (medium quality)
	termQueries := make([]query.Query, 0, len(terms))
	for _, term := range terms {
		termQueries = append(termQueries, addNameQuery(term, "name"))
	}
	interpretations = append(interpretations, QueryInterpretation{
		Description: "separate terms",
		Queries:     termQueries,
		Penalty:     0.1, // Small penalty for word breaks
	})

	// Interpretation 4: First term as category, rest as name (e.g. "restaurant Berlin")
	if CategoryMapper != nil {
		catMatches := CategoryMapper(terms[0])
		if len(catMatches) > 0 && len(terms) > 1 {
			catQueries := make([]query.Query, 0, len(catMatches)*2)
			for _, m := range catMatches {
				cq1 := bleve.NewMatchQuery(m.Value)
				cq1.SetField("value")
				cq2 := bleve.NewMatchQuery(m.Value)
				cq2.SetField("values")
				catQueries = append(catQueries, cq1, cq2)
			}
			nameQuery := addNameQuery(strings.Join(terms[1:], " "), "name")
			interpretations = append(interpretations, QueryInterpretation{
				Description: "category + name",
				Queries:     []query.Query{bleve.NewDisjunctionQuery(catQueries...), nameQuery},
				Penalty:     0.1,
			})
		}
	}

	// Interpretation 5: First term as primary, rest as qualifiers
	// This handles cases like "Berlin Mitte" where first is city, second is district
	if len(terms) >= 2 {
		primaryQuery := addNameQuery(terms[0], "name")
		qualifierQueries := []query.Query{primaryQuery}
		for _, term := range terms[1:] {
			qualifierQueries = append(qualifierQueries, addNameQuery(term, "name"))
		}
		interpretations = append(interpretations, QueryInterpretation{
			Description: "primary + qualifiers",
			Queries:     qualifierQueries,
			Penalty:     0.2, // Higher penalty for asymmetric split
		})
	}

	return interpretations
}

// executeInterpretations runs multiple interpretations of a query and returns the best results.
// Results are chosen based on result count and interpretation penalty.
func executeInterpretations(index bleve.Index, params SearchParams) (*bleve.SearchResult, error) {
	interpretations := generateInterpretations(params.Query)
	if len(interpretations) == 0 {
		return &bleve.SearchResult{}, nil
	}

	// Option A: Execute each and pick the winner
	// Option B: Combine into one DisjunctionQuery with boosts (new)
	// We use Option B to "interpret as possibly being multiple things" simultaneously.

	interpQueries := make([]query.Query, 0, len(interpretations))
	for _, interp := range interpretations {
		var combined query.Query
		if len(interp.Queries) == 1 {
			combined = interp.Queries[0]
		} else {
			combined = bleve.NewConjunctionQuery(interp.Queries...)
		}

		// Apply penalty as a negative boost
		// 1.0 - penalty (e.g., 0.0 penalty -> 1.0 boost, 0.2 penalty -> 0.8 boost)
		boost := 1.0 - interp.Penalty
		if boost < 0.1 {
			boost = 0.1 // Minimum boost
		}
		if b, ok := combined.(Boostable); ok {
			b.SetBoost(boost)
		}
		interpQueries = append(interpQueries, combined)
	}

	finalQuery := bleve.NewDisjunctionQuery(interpQueries...)

	searchRequest := bleve.NewSearchRequest(finalQuery)
	originalLimit := params.Limit
	if originalLimit <= 0 {
		originalLimit = 100
	}
	if originalLimit > 1000 {
		originalLimit = 1000
	}
	searchRequest.Size = min(originalLimit*3, 2000)
	searchRequest.From = params.From

	// Configure result fields
	searchRequest.Fields = []string{"*", "-geometry"}
	searchRequest.IncludeLocations = false
	searchRequest.SortBy([]string{"_score"})

	res, err := index.Search(searchRequest)
	if err != nil {
		return nil, err
	}

	return reRankAndTruncate(res, originalLimit, params.PopBoost), nil
}

// shouldUseMultiInterpretation returns true if the query should be parsed
// with multiple interpretations.
func shouldUseMultiInterpretation(params SearchParams) bool {
	// Don't use multi-interpretation if user explicitly requested fuzzy/prefix
	// (they already specified the matching strategy)
	if params.Fuzzy || params.Prefix {
		return false
	}

	// Always allow for queries with at least one word
	return params.Query != ""
}

package search

import (
	"sort"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

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
var CategoryMapper func(query string) []CategoryMatch

// generateInterpretations creates multiple interpretations of a multi-word query.
// Inspired by Nominatim's query DAG approach where "Berlin Mitte" could be:
// 1. [name="Berlin Mitte"] (full phrase)
// 2. [name="Berlin", name="Mitte"] (separate terms)
// 3. [name="Berlin", address="Mitte"] (city + district)
func generateInterpretations(q string, params SearchParams, analyzer string) []QueryInterpretation {
	terms := strings.Fields(q)
	if len(terms) == 0 {
		return nil
	}

	interpretations := make([]QueryInterpretation, 0, 5)

	// Interpretation 1: Full phrase match (highest quality, lowest penalty)
	fullQuery := addNameQuery(q, params.Fuzzy, params.Prefix, "name", analyzer)
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
		termQueries = append(termQueries, addNameQuery(term, params.Fuzzy, false, "name", analyzer))
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
			nameQuery := addNameQuery(strings.Join(terms[1:], " "), params.Fuzzy, params.Prefix, "name", analyzer)
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
		primaryQuery := addNameQuery(terms[0], params.Fuzzy, params.Prefix, "name", analyzer)
		qualifierQueries := []query.Query{primaryQuery}
		for _, term := range terms[1:] {
			qualifierQueries = append(qualifierQueries, addNameQuery(term, false, false, "name", analyzer))
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
	analyzer := params.Analyzer
	if analyzer == "" {
		analyzer = "standard"
	}

	interpretations := generateInterpretations(params.Query, params, analyzer)

	// Execute each interpretation and collect results
	type scoredResult struct {
		Interpretation QueryInterpretation
		Result         *bleve.SearchResult
		CombinedScore  float64
	}

	results := make([]scoredResult, 0, len(interpretations))

	for _, interp := range interpretations {
		// Build combined query for this interpretation
		var combinedQuery query.Query
		if len(interp.Queries) == 1 {
			combinedQuery = interp.Queries[0]
		} else {
			combinedQuery = bleve.NewDisjunctionQuery(interp.Queries...)
		}

		// Execute search
		searchRequest := bleve.NewSearchRequest(combinedQuery)
		searchRequest.Size = params.Limit
		if params.Limit <= 0 {
			searchRequest.Size = 50
		}

		// Configure result fields
		searchRequest.Fields = []string{"*", "-geometry"}
		searchRequest.IncludeLocations = false

		// Execute
		searchResult, err := index.Search(searchRequest)
		if err != nil {
			continue
		}

		// Calculate combined score: results * (1 - penalty)
		combinedScore := float64(searchResult.Total) * (1.0 - interp.Penalty)

		results = append(results, scoredResult{
			Interpretation: interp,
			Result:         searchResult,
			CombinedScore:  combinedScore,
		})
	}

	// Sort by combined score (descending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].CombinedScore > results[j].CombinedScore
	})

	// Return the best interpretation's results
	if len(results) > 0 {
		return results[0].Result, nil
	}

	// Fallback: empty results
	return &bleve.SearchResult{}, nil
}

// shouldUseMultiInterpretation returns true if the query should be parsed
// with multiple interpretations. Only applies to queries with 2+ words
// that don't already have specific modifiers (fuzzy, prefix, etc.).
func shouldUseMultiInterpretation(params SearchParams) bool {
	// Only for multi-word queries
	if len(strings.Fields(params.Query)) < 2 {
		return false
	}

	// Don't use multi-interpretation if user explicitly requested fuzzy/prefix
	// (they already specified the matching strategy)
	if params.Fuzzy || params.Prefix {
		return false
	}

	return true
}

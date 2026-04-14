package search

import (
	"sort"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

// termFrequency represents the estimated frequency of a search term.
type termFrequency struct {
	Term      string
	Length    int
	IsRare    bool
}

// analyzeTermFrequencies estimates the frequency of each term in the query.
// Uses heuristics based on term length and common words.
// Shorter terms are typically more common; longer terms are rarer.
func analyzeTermFrequencies(terms []string) []termFrequency {
	// Common words that appear frequently in POI names
	commonWords := map[string]bool{
		"the": true, "of": true, "in": true, "and": true,
		"st": true, "street": true, "road": true, "ave": true,
		"city": true, "town": true, "village": true,
	}

	freqs := make([]termFrequency, 0, len(terms))
	for _, term := range terms {
		tf := termFrequency{
			Term:   term,
			Length: len(term),
		}

		// Heuristics for term rarity:
		// 1. Common words are always frequent
		// 2. Short words (< 3 chars) are usually frequent
		// 3. Longer words (> 6 chars) are usually rare
		if commonWords[term] || len(term) < 3 {
			tf.IsRare = false
		} else if len(term) > 6 {
			tf.IsRare = true
		} else {
			// Medium length: use length as proxy
			tf.IsRare = len(term) >= 5
		}

		freqs = append(freqs, tf)
	}

	// Sort by rarity: rare terms first (they're more selective)
	sort.Slice(freqs, func(i, j int) bool {
		if freqs[i].IsRare != freqs[j].IsRare {
			return freqs[i].IsRare
		}
		// Within same rarity, longer terms first (more selective)
		return freqs[i].Length > freqs[j].Length
	})

	return freqs
}

// buildFrequencyAwareQuery creates a query that uses rare terms for index lookup
// and common terms for post-filtering. This improves performance by:
// 1. Using rare/selective terms for the initial index lookup (fast)
// 2. Using common terms as post-filters (avoids expensive index scans)
//
// Inspired by Nominatim's CountedTokenIDs optimization.
func buildFrequencyAwareQuery(q string, params SearchParams, analyzer string) query.Query {
	terms := strings.Fields(q)
	if len(terms) == 0 {
		return bleve.NewMatchAllQuery()
	}

	// Analyze term frequencies
	freqs := analyzeTermFrequencies(terms)

	// Split into rare (for index lookup) and common (for post-filter)
	var rareTerms []string
	var commonTerms []string
	for _, tf := range freqs {
		if tf.IsRare {
			rareTerms = append(rareTerms, tf.Term)
		} else {
			commonTerms = append(commonTerms, tf.Term)
		}
	}

	// If no rare terms, use all terms as rare
	if len(rareTerms) == 0 {
		rareTerms = terms
		commonTerms = nil
	}

	// Build queries for rare terms (will be used for index lookup)
	rareQueries := make([]query.Query, 0, len(rareTerms))
	for _, term := range rareTerms {
		q := addNameQuery(term, params.Fuzzy, params.Prefix, "name", analyzer)
		rareQueries = append(rareQueries, q)
	}

	// If only rare terms, use conjunction (AND) for maximum selectivity
	if len(commonTerms) == 0 {
		if len(rareQueries) == 1 {
			return rareQueries[0]
		}
		return bleve.NewConjunctionQuery(rareQueries...)
	}

	// Build queries for common terms (will be used as post-filters)
	commonQueries := make([]query.Query, 0, len(commonTerms))
	for _, term := range commonTerms {
		q := addNameQuery(term, false, false, "name", analyzer)
		commonQueries = append(commonQueries, q)
	}

	// Combine: rare terms (conjunction) AND common terms (conjunction)
	// This ensures rare terms drive the index lookup
	allQueries := append(rareQueries, commonQueries...)
	return bleve.NewConjunctionQuery(allQueries...)
}

// optimizeQueryTerms reorders query terms for optimal Bleve performance.
// Rare/selective terms are placed first to reduce the candidate set early.
func optimizeQueryTerms(terms []string) []string {
	freqs := analyzeTermFrequencies(terms)
	result := make([]string, 0, len(freqs))
	for _, tf := range freqs {
		result = append(result, tf.Term)
	}
	return result
}

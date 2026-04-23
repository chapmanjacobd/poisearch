package tests_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

func TestUserFlow_SearchPizzaAndRestaurant(t *testing.T) {
	pbfPath := downloadPBF(t)
	conf := defaultTestConfig()
	conf.NameAnalyzer = "standard"
	// Enable metadata storage so we can verify the matched fields
	conf.StoreMetadata = true
	// Disable importance so true text matches (like category) outrank fuzzy matches on high-importance nodes
	conf.DisableImportance = true

	// Initialize CategoryMapper
	ont := osm.DefaultOntology()
	search.CategoryMapper = func(q string) []search.CategoryMatch {
		q = strings.ToLower(q)
		matches := ont.GetTagsForLabel(q)
		result := make([]search.CategoryMatch, 0, len(matches))
		for _, m := range matches {
			result = append(result, search.CategoryMatch{Key: m.Key, Value: m.Value})
		}
		return result
	}

	_, idx := buildTestIndex(t, pbfPath, conf)
	defer idx.Close()

	// Mimic user flow: searching for "pizza"
	params := search.SearchParams{
		Query:    "pizza",
		Limit:    10,
		GeoMode:  "geopoint-centroid",
		Langs:    conf.Languages,
		Analyzer: conf.NameAnalyzer,
	}

	results, err := search.Search(idx, params)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(results.Hits) == 0 {
		t.Fatalf("Expected some results for 'pizza'")
	}

	// Verify that the word "pizza" or "restaurant" is in the top 10 results' fields
	foundExpectedKeyword := false
	for i, hit := range results.Hits {
		fieldsStr := strings.ToLower(fmt.Sprintf("%v", hit.Fields))
		t.Logf("Top Hit %d: ID=%s (Score: %f) Fields: %s", i+1, hit.ID, hit.Score, fieldsStr)

		if strings.Contains(fieldsStr, "pizza") || strings.Contains(fieldsStr, "restaurant") {
			foundExpectedKeyword = true
			break
		}
	}

	if !foundExpectedKeyword {
		t.Errorf("Expected to find 'pizza' or 'restaurant' in the top results' fields, but did not.")
	}
}

func TestUserFlow_StructuredSearch(t *testing.T) {
	pbfPath := downloadPBF(t)
	conf := defaultTestConfig()
	conf.NameAnalyzer = "standard"
	conf.StoreMetadata = true
	conf.StoreAddress = true

	_, idx := buildTestIndex(t, pbfPath, conf)
	defer idx.Close()

	// Mimic a structured search where user provided explicit filters: "restaurant postcode=9490"
	params := search.SearchParams{
		Query:    "restaurant",
		Postcode: "9490", // 9490 is Vaduz
		Limit:    10,
		GeoMode:  "geopoint-centroid",
		Langs:    conf.Languages,
		Analyzer: conf.NameAnalyzer,
	}

	results, err := search.Search(idx, params)
	if err != nil {
		t.Fatalf("structured search failed: %v", err)
	}

	if len(results.Hits) == 0 {
		t.Fatalf("No results found for structured search in this PBF. Expected at least one restaurant in 9490.")
	}

	t.Logf("Structured search returned %d results.", len(results.Hits))
	for i, hit := range results.Hits {
		t.Logf("Result %d: ID=%s Fields=%v", i+1, hit.ID, hit.Fields)
	}
}

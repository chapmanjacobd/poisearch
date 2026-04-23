package tests_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

func TestUserFlow_PMTilesGlobalSearch(t *testing.T) {
	pmtilesPath := "../liechtenstein.pmtiles"
	if _, err := os.Stat(pmtilesPath); err != nil {
		t.Fatalf("PMTiles file not found at %s. Run scripts/pbf_to_pmtiles.sh to generate it.", pmtilesPath)
	}

	conf := defaultTestConfig()
	conf.NameAnalyzer = "standard"
	// PMTiles direct search builds result hits using essentially the same fields logic
	conf.StoreMetadata = true

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

	// For PMTiles we usually need a bbox or radius if we want to limit the search,
	// but a global search works too (though it scans the whole file).
	// liechtenstein.pmtiles is small enough for a global search.
	params := search.SearchParams{
		Query:   "pizza",
		Limit:   10,
		GeoMode: "geopoint-centroid",
		Langs:   conf.Languages,
	}

	results, err := osm.PMTilesSearch(pmtilesPath, params, conf)
	if err != nil {
		t.Fatalf("PMTilesSearch failed: %v", err)
	}

	if len(results.Hits) == 0 {
		t.Fatalf("Expected some results for 'pizza' via PMTiles")
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
		t.Errorf("Expected to find 'pizza' or 'restaurant' in the top results' fields via PMTiles search, but did not.")
	}
}

func TestUserFlow_PMTilesStructuredSearch(t *testing.T) {
	pmtilesPath := "../liechtenstein.pmtiles"
	if _, err := os.Stat(pmtilesPath); err != nil {
		t.Fatalf("PMTiles file not found at %s. Run scripts/pbf_to_pmtiles.sh to generate it.", pmtilesPath)
	}

	conf := defaultTestConfig()
	conf.NameAnalyzer = "standard"
	conf.StoreMetadata = true

	// Scenario 1: Category filter - "Find all restaurants"
	t.Run("CategoryFilter", func(t *testing.T) {
		params := search.SearchParams{
			Value:   "restaurant",
			Limit:   10,
			GeoMode: "geopoint-centroid",
			Langs:   conf.Languages,
		}

		results, err := osm.PMTilesSearch(pmtilesPath, params, conf)
		if err != nil {
			t.Fatalf("PMTiles structured search failed: %v", err)
		}

		if len(results.Hits) == 0 {
			t.Fatalf("Expected at least one restaurant via category filter")
		}

		t.Logf("Category search (restaurant) returned %d results.", len(results.Hits))
		for _, hit := range results.Hits {
			if hit.Fields["value"] != "restaurant" {
				t.Errorf("Expected result to have value=restaurant, got %v", hit.Fields["value"])
			}
		}
	})

	// Scenario 2: Combined Search - "Pizza near Vaduz"
	t.Run("PizzaNearVaduz", func(t *testing.T) {
		// Vaduz coordinates
		lat := 47.14
		lon := 9.52
		params := search.SearchParams{
			Query:   "pizza",
			Lat:     &lat,
			Lon:     &lon,
			Radius:  "2km",
			Limit:   10,
			GeoMode: "geopoint-centroid",
			Langs:   conf.Languages,
		}

		results, err := osm.PMTilesSearch(pmtilesPath, params, conf)
		if err != nil {
			t.Fatalf("PMTiles search failed: %v", err)
		}

		if len(results.Hits) == 0 {
			t.Fatalf("Expected some results for 'pizza' near Vaduz")
		}

		t.Logf("Spatial search (pizza near Vaduz) returned %d results.", len(results.Hits))
		for _, hit := range results.Hits {
			name := strings.ToLower(fmt.Sprintf("%v", hit.Fields["name"]))
			if !strings.Contains(name, "pizza") &&
				!strings.Contains(name, "azzurro") { // Azzurro is a pizza place in Vaduz

				t.Errorf("Result %s does not seem to match 'pizza'", name)
			}
		}
	})

	t.Run("NaturalLanguagePizzaNearVaduz", func(t *testing.T) {
		params := search.SearchParams{
			Query:   "pizza near Vaduz",
			Limit:   10,
			GeoMode: "geopoint-centroid",
			Langs:   conf.Languages,
		}

		results, err := osm.PMTilesSearch(pmtilesPath, params, conf)
		if err != nil {
			t.Fatalf("PMTiles near search failed: %v", err)
		}

		if len(results.Hits) == 0 {
			t.Fatalf("Expected some results for natural-language query 'pizza near Vaduz'")
		}

		t.Logf("Natural-language PMTiles near search returned %d results.", len(results.Hits))
		for _, hit := range results.Hits {
			name := strings.ToLower(fmt.Sprintf("%v", hit.Fields["name"]))
			if !strings.Contains(name, "pizza") &&
				!strings.Contains(name, "azzurro") {

				t.Errorf("Result %s does not seem to match 'pizza'", name)
			}
		}
	})
}

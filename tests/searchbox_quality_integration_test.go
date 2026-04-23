package tests_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/blevesearch/bleve/v2"
	bleveSearch "github.com/blevesearch/bleve/v2/search"
	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

func TestSearchboxQuality_Liechtenstein(t *testing.T) {
	pbfPath := downloadPBF(t)
	pmtilesPath := "../liechtenstein.pmtiles"
	if _, err := os.Stat(pmtilesPath); err != nil {
		t.Fatalf("PMTiles file not found at %s. Run scripts/pbf_to_pmtiles.sh to generate it.", pmtilesPath)
	}

	conf := defaultTestConfig()
	conf.NameAnalyzer = "edge_ngram"
	conf.StoreMetadata = true
	conf.StoreAddress = true
	conf.GeometryMode = "geopoint-centroid"

	initTestCategoryMapper()

	_, idx := buildTestIndex(t, pbfPath, conf)
	defer idx.Close()

	type modeCase struct {
		name                 string
		search               func(t *testing.T, params search.SearchParams) *bleve.SearchResult
		expectedPizzaMatches int
	}

	modes := []modeCase{
		{
			name: "bleve",
			search: func(t *testing.T, params search.SearchParams) *bleve.SearchResult {
				t.Helper()
				results, err := search.Search(idx, params)
				if err != nil {
					t.Fatalf("bleve search failed: %v", err)
				}
				return results
			},
			expectedPizzaMatches: 3,
		},
		{
			name: "pbf",
			search: func(t *testing.T, params search.SearchParams) *bleve.SearchResult {
				t.Helper()
				results, err := osm.PBFSearch(pbfPath, params, conf)
				if err != nil {
					t.Fatalf("PBF search failed: %v", err)
				}
				return results
			},
			expectedPizzaMatches: 3,
		},
		{
			name: "pmtiles",
			search: func(t *testing.T, params search.SearchParams) *bleve.SearchResult {
				t.Helper()
				results, err := osm.PMTilesSearch(pmtilesPath, params, conf)
				if err != nil {
					t.Fatalf("PMTiles search failed: %v", err)
				}
				return results
			},
			expectedPizzaMatches: 2,
		},
	}

	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			t.Run("pizza-global", func(t *testing.T) {
				results := mode.search(t, search.SearchParams{
					Query:    "pizza",
					Limit:    5,
					GeoMode:  "geopoint-centroid",
					Langs:    conf.Languages,
					Analyzer: conf.NameAnalyzer,
				})

				assertTopResultsRelevant(t, results, 5, mode.expectedPizzaMatches, pizzaRelevantHit)
				assertTopNamesContain(t, results, 5, mode.expectedPizzaMatches,
					"azzurro pizza", "pizza e pinsa", "pizzeria toscana", "pizzeria il salento", "pizzeria pronto")
			})

			t.Run("pizza-near-vaduz", func(t *testing.T) {
				lat := 47.14
				lon := 9.52
				results := mode.search(t, search.SearchParams{
					Query:    "pizza",
					Lat:      &lat,
					Lon:      &lon,
					Radius:   "2km",
					Limit:    5,
					GeoMode:  "geopoint-centroid",
					Langs:    conf.Languages,
					Analyzer: conf.NameAnalyzer,
				})

				assertTopResultsRelevant(t, results, 3, 2, pizzaRelevantHit)
			})

			t.Run("restaurant-global", func(t *testing.T) {
				results := mode.search(t, search.SearchParams{
					Query:    "restaurant",
					Limit:    5,
					GeoMode:  "geopoint-centroid",
					Langs:    conf.Languages,
					Analyzer: conf.NameAnalyzer,
				})

				assertTopResultsRelevant(t, results, 5, 4, restaurantRelevantHit)
			})

			t.Run("place-vaduz", func(t *testing.T) {
				results := mode.search(t, search.SearchParams{
					Query:    "Vaduz",
					Limit:    5,
					GeoMode:  "geopoint-centroid",
					Langs:    conf.Languages,
					Analyzer: conf.NameAnalyzer,
				})

				assertTopResultsRelevant(t, results, 3, 1, placeRelevantHit)
			})
		})
	}
}

func initTestCategoryMapper() {
	ont := osm.DefaultOntology()
	search.CategoryMapper = func(q string) []search.CategoryMatch {
		q = strings.ToLower(strings.TrimSpace(q))
		matches := ont.GetTagsForLabel(q)
		if len(matches) == 0 && strings.HasSuffix(q, "s") {
			matches = ont.GetTagsForLabel(q[:len(q)-1])
		}
		result := make([]search.CategoryMatch, 0, len(matches))
		for _, m := range matches {
			result = append(result, search.CategoryMatch{Key: m.Key, Value: m.Value})
		}
		return result
	}
}

func assertTopResultsRelevant(
	t *testing.T,
	results *bleve.SearchResult,
	topN int,
	minRelevant int,
	isRelevant func(*bleve.SearchResult, *bleveSearch.DocumentMatch) bool,
) {
	t.Helper()

	if len(results.Hits) == 0 {
		t.Fatal("expected search results, got none")
	}

	limit := min(topN, len(results.Hits))
	relevant := 0
	for i := range limit {
		hit := results.Hits[i]
		t.Logf("Top hit %d: score=%f id=%s fields=%v", i+1, hit.Score, hit.ID, hit.Fields)
		if isRelevant(results, hit) {
			relevant++
		}
	}

	if !isRelevant(results, results.Hits[0]) {
		t.Fatalf("expected first hit to be relevant, got %s (%v)", results.Hits[0].ID, results.Hits[0].Fields)
	}
	if relevant < minRelevant {
		t.Fatalf("expected at least %d relevant hits in top %d, got %d", minRelevant, limit, relevant)
	}
}

func assertTopNamesContain(t *testing.T, results *bleve.SearchResult, topN, minMatches int, needles ...string) {
	t.Helper()

	limit := min(topN, len(results.Hits))
	matches := 0
	for i := range limit {
		name := strings.ToLower(fmt.Sprint(results.Hits[i].Fields["name"]))
		for _, needle := range needles {
			if strings.Contains(name, needle) {
				matches++
				break
			}
		}
	}

	if matches < minMatches {
		t.Fatalf("expected at least %d expected names in top %d results, got %d", minMatches, limit, matches)
	}
}

func pizzaRelevantHit(_ *bleve.SearchResult, hit *bleveSearch.DocumentMatch) bool {
	fields := strings.ToLower(fmt.Sprintf("%v", hit.Fields))
	return strings.Contains(fields, "pizza") ||
		strings.Contains(fields, "pizzeria") ||
		strings.Contains(fields, "pinsa") ||
		strings.Contains(fields, "kebap")
}

func restaurantRelevantHit(_ *bleve.SearchResult, hit *bleveSearch.DocumentMatch) bool {
	fields := strings.ToLower(fmt.Sprintf("%v", hit.Fields))
	if strings.Contains(fields, "value:restaurant") {
		return true
	}
	name := strings.ToLower(fmt.Sprint(hit.Fields["name"]))
	return strings.Contains(name, "restaurant") ||
		strings.Contains(name, "pizzeria") ||
		strings.Contains(name, "brasserie")
}

func placeRelevantHit(_ *bleve.SearchResult, hit *bleveSearch.DocumentMatch) bool {
	name := strings.ToLower(strings.TrimSpace(fmt.Sprint(hit.Fields["name"])))
	return name == "vaduz"
}

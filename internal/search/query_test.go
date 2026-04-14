package search_test

import (
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

func createTestIndexForQuery(t *testing.T) bleve.Index {
	t.Helper()

	conf := &config.Config{
		Languages:      []string{"en"},
		StoreAddress:   true,
		StoreGeometry:  true,
		StoreMetadata:  true,
		GeometryMode:   "geopoint",
		NameAnalyzer:   "standard",
	}

	indexMapping := search.BuildIndexMapping(conf)

	index, err := bleve.NewMemOnly(indexMapping)
	if err != nil {
		t.Fatalf("failed to create test index: %v", err)
	}

	// Index test documents
	testDocs := []struct {
		id         string
		name       string
		class      string
		subtype    string
		importance float64
		lat, lon   float64
		street     string
		housenum   string
		postcode   string
		city       string
		country    string
	}{
		{"node/1", "Berlin", "place", "city", 10.0, 52.52, 13.40, "Unter den Linden", "1", "10117", "Berlin", "DE"},
		{"node/2", "Munich", "place", "city", 9.0, 48.13, 11.58, "Marienplatz", "1", "80331", "Munich", "DE"},
		{
			"node/3",
			"Restaurant Alpha",
			"amenity",
			"restaurant",
			5.0,
			52.53,
			13.41,
			"Friedrichstr",
			"10",
			"10117",
			"Berlin",
			"DE",
		},
		{"node/4", "Cafe Beta", "amenity", "cafe", 4.0, 52.54, 13.42, "", "", "", "", ""},
		{"node/5", "Shop Gamma", "shop", "yes", 3.0, 48.14, 11.59, "", "", "80331", "", "DE"},
	}

	for _, doc := range testDocs {
		data := map[string]any{
			"name":       doc.name,
			"class":      doc.class,
			"subtype":    doc.subtype,
			"importance": doc.importance,
			"geometry":   []float64{doc.lon, doc.lat},
		}
		if doc.street != "" {
			data["addr:street"] = doc.street
		}
		if doc.housenum != "" {
			data["addr:housenumber"] = doc.housenum
		}
		if doc.city != "" {
			data["addr:city"] = doc.city
		}
		if doc.postcode != "" {
			data["addr:postcode"] = doc.postcode
		}
		if doc.country != "" {
			data["addr:country"] = doc.country
		}
		if err := index.Index(doc.id, data); err != nil {
			t.Fatalf("failed to index document %s: %v", doc.id, err)
		}
	}

	return index
}

func TestSearch_Pagination(t *testing.T) {
	index := createTestIndexForQuery(t)
	defer index.Close()

	tests := []struct {
		name       string
		params     search.SearchParams
		expectHits int
		expectFrom int
	}{
		{
			name: "first page limit 2",
			params: search.SearchParams{
				Query:    "",
				Limit:    2,
				From:     0,
				Langs:    []string{"en"},
				GeoMode:  "geopoint",
				Analyzer: "standard",
			},
			expectHits: 2,
			expectFrom: 0,
		},
		{
			name: "second page from 2",
			params: search.SearchParams{
				Query:    "",
				Limit:    2,
				From:     2,
				Langs:    []string{"en"},
				GeoMode:  "geopoint",
				Analyzer: "standard",
			},
			expectHits: 2,
			expectFrom: 2,
		},
		{
			name: "third page from 4",
			params: search.SearchParams{
				Query:    "",
				Limit:    2,
				From:     4,
				Langs:    []string{"en"},
				GeoMode:  "geopoint",
				Analyzer: "standard",
			},
			expectHits: 1,
			expectFrom: 4,
		},
		{
			name: "beyond results from 10",
			params: search.SearchParams{
				Query:    "",
				Limit:    10,
				From:     10,
				Langs:    []string{"en"},
				GeoMode:  "geopoint",
				Analyzer: "standard",
			},
			expectHits: 0,
			expectFrom: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := search.Search(index, tt.params)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			if len(results.Hits) != tt.expectHits {
				t.Errorf("expected %d hits, got %d", tt.expectHits, len(results.Hits))
			}
		})
	}
}

func TestSearch_AddressFilters(t *testing.T) {
	index := createTestIndexForQuery(t)
	defer index.Close()

	tests := []struct {
		name      string
		params    search.SearchParams
		expectMin int
	}{
		{
			name: "filter by street",
			params: search.SearchParams{
				Query:   "",
				Street:  "Unter den Linden",
				Limit:   10,
				Langs:   []string{"en"},
				GeoMode: "geopoint",
			},
			expectMin: 1,
		},
		{
			name: "filter by housenumber",
			params: search.SearchParams{
				Query:       "",
				HouseNumber: "1",
				Limit:       10,
				Langs:       []string{"en"},
				GeoMode:     "geopoint",
			},
			expectMin: 1,
		},
		{
			name: "filter by postcode",
			params: search.SearchParams{
				Query:    "",
				Postcode: "10117",
				Limit:    10,
				Langs:    []string{"en"},
				GeoMode:  "geopoint",
			},
			expectMin: 2,
		},
		{
			name: "filter by city",
			params: search.SearchParams{
				Query:   "",
				City:    "Berlin",
				Limit:   10,
				Langs:   []string{"en"},
				GeoMode: "geopoint",
			},
			expectMin: 2,
		},
		{
			name: "filter by country",
			params: search.SearchParams{
				Query:   "",
				Country: "DE",
				Limit:   10,
				Langs:   []string{"en"},
				GeoMode: "geopoint",
			},
			expectMin: 3, // Only 3 docs have address fields populated
		},
		{
			name: "filter by city case-insensitive",
			params: search.SearchParams{
				Query:   "",
				City:    "berlin", // Lowercase
				Limit:   10,
				Langs:   []string{"en"},
				GeoMode: "geopoint",
			},
			expectMin: 2,
		},
		{
			name: "filter by country case-insensitive",
			params: search.SearchParams{
				Query:   "",
				Country: "de", // Lowercase
				Limit:   10,
				Langs:   []string{"en"},
				GeoMode: "geopoint",
			},
			expectMin: 3,
		},
		{
			name: "filter by street and city",
			params: search.SearchParams{
				Query:   "",
				Street:  "Unter den Linden",
				City:    "Berlin",
				Limit:   10,
				Langs:   []string{"en"},
				GeoMode: "geopoint",
			},
			expectMin: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := search.Search(index, tt.params)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			if int(results.Total) < tt.expectMin {
				t.Errorf("expected at least %d results, got %d", tt.expectMin, results.Total)
			}
		})
	}
}

func TestSearch_MultiClassSubtypeFilters(t *testing.T) {
	index := createTestIndexForQuery(t)
	defer index.Close()

	tests := []struct {
		name      string
		params    search.SearchParams
		expectMin int
	}{
		{
			name: "single class filter",
			params: search.SearchParams{
				Query:   "",
				Class:   "amenity",
				Limit:   10,
				Langs:   []string{"en"},
				GeoMode: "geopoint",
			},
			expectMin: 2,
		},
		{
			name: "multi-class filter OR",
			params: search.SearchParams{
				Query:   "",
				Classes: []string{"amenity", "shop"},
				Limit:   10,
				Langs:   []string{"en"},
				GeoMode: "geopoint",
			},
			expectMin: 3,
		},
		{
			name: "single subtype filter",
			params: search.SearchParams{
				Query:   "",
				Subtype: "restaurant",
				Limit:   10,
				Langs:   []string{"en"},
				GeoMode: "geopoint",
			},
			expectMin: 1,
		},
		{
			name: "multi-subtype filter OR",
			params: search.SearchParams{
				Query:    "",
				Subtypes: []string{"restaurant", "cafe"},
				Limit:    10,
				Langs:    []string{"en"},
				GeoMode:  "geopoint",
			},
			expectMin: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := search.Search(index, tt.params)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			if int(results.Total) < tt.expectMin {
				t.Errorf("expected at least %d results, got %d", tt.expectMin, results.Total)
			}
		})
	}
}

func TestSearch_FuzzyAndPrefix(t *testing.T) {
	index := createTestIndexForQuery(t)
	defer index.Close()

	tests := []struct {
		name      string
		params    search.SearchParams
		expectMin int
	}{
		{
			name: "fuzzy search",
			params: search.SearchParams{
				Query:    "Brlin", // Misspelled
				Fuzzy:    true,
				Limit:    10,
				Langs:    []string{"en"},
				GeoMode:  "geopoint",
				Analyzer: "standard",
			},
			expectMin: 1, // Should fuzzy-match Berlin
		},
		{
			name: "prefix search",
			params: search.SearchParams{
				Query:    "Ber",
				Prefix:   true,
				Limit:    10,
				Langs:    []string{"en"},
				GeoMode:  "geopoint",
				Analyzer: "standard",
			},
			expectMin: 1, // Should prefix-match Berlin
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := search.Search(index, tt.params)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			if int(results.Total) < tt.expectMin {
				t.Errorf("expected at least %d results, got %d", tt.expectMin, results.Total)
			}
		})
	}
}

func TestSearch_GeoFilters(t *testing.T) {
	index := createTestIndexForQuery(t)
	defer index.Close()

	lat := 52.52
	lon := 13.40

	tests := []struct {
		name      string
		params    search.SearchParams
		expectMin int
	}{
		{
			name: "radius search 1km",
			params: search.SearchParams{
				Query:   "",
				Lat:     &lat,
				Lon:     &lon,
				Radius:  "1km",
				Limit:   10,
				Langs:   []string{"en"},
				GeoMode: "geopoint",
			},
			expectMin: 1,
		},
		{
			name: "radius search 50km",
			params: search.SearchParams{
				Query:   "",
				Lat:     &lat,
				Lon:     &lon,
				Radius:  "50km",
				Limit:   10,
				Langs:   []string{"en"},
				GeoMode: "geopoint",
			},
			expectMin: 3,
		},
		{
			name: "bbox search Berlin area",
			params: search.SearchParams{
				Query:   "",
				MinLat:  new(52.4),
				MaxLat:  new(52.6),
				MinLon:  new(13.3),
				MaxLon:  new(13.5),
				Limit:   10,
				Langs:   []string{"en"},
				GeoMode: "geopoint",
			},
			expectMin: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := search.Search(index, tt.params)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			if int(results.Total) < tt.expectMin {
				t.Errorf("expected at least %d results, got %d", tt.expectMin, results.Total)
			}
		})
	}
}

func floatPtr(f float64) *float64 {
	return new(f)
}

// TestSearchParams_QueryFields tests the QueryFields helper method.
func TestSearchParams_QueryFields(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  int
	}{
		{"empty query", "", 0},
		{"single word", "Berlin", 1},
		{"two words", "New York", 2},
		{"multiple words", "Restaurant in Berlin", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := search.SearchParams{Query: tt.query}
			got := params.QueryFields()
			if got != tt.want {
				t.Errorf("QueryFields() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestMatchTierBoost tests the match tier boost values.
func TestMatchTierBoost(t *testing.T) {
	tests := []struct {
		tier search.MatchTier
		want float64
	}{
		{search.TierExact, 3.0},
		{search.TierPrefix, 2.0},
		{search.TerFuzzy, 1.0},
		{search.MatchTier(-1), 1.0}, // Invalid tier
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := search.MatchTierBoost(tt.tier)
			if got != tt.want {
				t.Errorf("MatchTierBoost(%v) = %f, want %f", tt.tier, got, tt.want)
			}
		})
	}
}

package osm

import (
	"testing"

	"github.com/blevesearch/bleve/v2"
	bleveSearch "github.com/blevesearch/bleve/v2/search"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

func TestDirectNearSearch_PreservesSecondPhaseFilters(t *testing.T) {
	baseParams := search.SearchParams{
		Query:        "pizza near Vaduz",
		Limit:        7,
		From:         2,
		Fuzzy:        true,
		Prefix:       true,
		Key:          "amenity",
		Value:        "restaurant",
		Keys:         []string{"amenity", "shop"},
		Values:       []string{"restaurant", "pizza"},
		Phone:        "123",
		Wheelchair:   "yes",
		OpeningHours: "24/7",
		GeoMode:      "geopoint-centroid",
		Langs:        []string{"en"},
		Analyzer:     "standard",
		ExactMatch:   true,
	}

	var calls []search.SearchParams
	searchFn := func(params search.SearchParams) (*bleve.SearchResult, error) {
		calls = append(calls, params)
		switch len(calls) {
		case 1:
			return &bleve.SearchResult{
				Hits: []*bleveSearch.DocumentMatch{
					{
						ID: "node/1",
						Fields: map[string]any{
							"geometry": map[string]any{"lat": 47.14, "lon": 9.52},
						},
					},
				},
			}, nil
		case 2:
			return &bleve.SearchResult{}, nil
		default:
			t.Fatalf("unexpected extra direct search call: %d", len(calls))
			return nil, nil
		}
	}

	if _, err := directNearSearch(baseParams, searchFn); err != nil {
		t.Fatalf("directNearSearch failed: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 direct search calls, got %d", len(calls))
	}

	if calls[0].Query != "Vaduz" {
		t.Fatalf("reference query = %q, want Vaduz", calls[0].Query)
	}
	if calls[0].Limit != 1 {
		t.Fatalf("reference limit = %d, want 1", calls[0].Limit)
	}

	second := calls[1]
	if second.Query != "pizza" {
		t.Fatalf("second-phase query = %q, want pizza", second.Query)
	}
	if second.Lat == nil || second.Lon == nil || *second.Lat != 47.14 || *second.Lon != 9.52 {
		t.Fatalf("second-phase coordinates = (%v, %v), want (47.14, 9.52)", second.Lat, second.Lon)
	}
	if second.Radius != "5000m" {
		t.Fatalf("second-phase radius = %q, want 5000m", second.Radius)
	}
	if second.Key != baseParams.Key || second.Value != baseParams.Value {
		t.Fatal("expected key/value filters to be preserved")
	}
	if second.Phone != baseParams.Phone || second.Wheelchair != baseParams.Wheelchair ||
		second.OpeningHours != baseParams.OpeningHours {
		t.Fatal("expected metadata filters to be preserved")
	}
	if second.From != baseParams.From || second.Limit != baseParams.Limit {
		t.Fatal("expected pagination to be preserved")
	}
	if !second.Fuzzy || !second.Prefix || !second.ExactMatch {
		t.Fatal("expected fuzzy/prefix/exact_match flags to be preserved")
	}
}

func TestDirectNearSearch_PreservesExplicitRadius(t *testing.T) {
	baseParams := search.SearchParams{
		Query:  "pizza near Vaduz",
		Radius: "2km",
	}

	var secondPhase search.SearchParams
	searchFn := func(params search.SearchParams) (*bleve.SearchResult, error) {
		if params.Query == "Vaduz" {
			return &bleve.SearchResult{
				Hits: []*bleveSearch.DocumentMatch{
					{
						Fields: map[string]any{
							"geometry": map[string]any{"lat": 47.14, "lon": 9.52},
						},
					},
				},
			}, nil
		}

		secondPhase = params
		return &bleve.SearchResult{}, nil
	}

	if _, err := directNearSearch(baseParams, searchFn); err != nil {
		t.Fatalf("directNearSearch failed: %v", err)
	}

	if secondPhase.Radius != "2km" {
		t.Fatalf("second-phase radius = %q, want 2km", secondPhase.Radius)
	}
}

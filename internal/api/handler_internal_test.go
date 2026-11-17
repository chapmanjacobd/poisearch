package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/config"
)

func TestParseSearchParams_ExactMatch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/search?q=park&exact_match=true", nil)
	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
	}

	params := parseSearchParams(req, conf)

	if !params.ExactMatch {
		t.Fatal("expected exact_match=true to set ExactMatch")
	}
}

func TestParseSearchParams_InfersHouseNumberFromStreet(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/search?street=Main+St+123", nil)
	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
	}

	params := parseSearchParams(req, conf)

	if params.Street != "Main St" {
		t.Fatalf("expected street to be normalized, got %q", params.Street)
	}
	if params.HouseNumber != "123" {
		t.Fatalf("expected housenumber to be inferred, got %q", params.HouseNumber)
	}
}

func TestParseSearchParams_InfersPrefixedHouseNumberFromStreet(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/search?street=123+Main+St", nil)
	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
	}

	params := parseSearchParams(req, conf)

	if params.Street != "Main St" {
		t.Fatalf("expected street to be normalized, got %q", params.Street)
	}
	if params.HouseNumber != "123" {
		t.Fatalf("expected housenumber to be inferred, got %q", params.HouseNumber)
	}
}

func TestParseSearchParams_StripsDuplicateHouseNumberFromStreet(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/search?street=Main+St+123&housenumber=123", nil)
	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
	}

	params := parseSearchParams(req, conf)

	if params.Street != "Main St" {
		t.Fatalf("expected street to drop trailing housenumber, got %q", params.Street)
	}
	if params.HouseNumber != "123" {
		t.Fatalf("expected explicit housenumber to be preserved, got %q", params.HouseNumber)
	}
}

func TestParseSearchParams_StripsDuplicatePrefixedHouseNumberFromStreet(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/search?street=123+Main+St&housenumber=123", nil)
	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
	}

	params := parseSearchParams(req, conf)

	if params.Street != "Main St" {
		t.Fatalf("expected street to drop prefixed housenumber, got %q", params.Street)
	}
	if params.HouseNumber != "123" {
		t.Fatalf("expected explicit housenumber to be preserved, got %q", params.HouseNumber)
	}
}

func TestParseSearchParams_TagFilters(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/search?amenity=restaurant&wheelchair=yes", nil)
	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
	}

	params := parseSearchParams(req, conf)

	if params.Tags["amenity"] != "restaurant" {
		t.Fatalf("expected amenity tag filter, got %v", params.Tags)
	}
	if params.Wheelchair != "yes" {
		t.Fatalf("expected wheelchair metadata filter, got %q", params.Wheelchair)
	}
	if _, ok := params.Tags["wheelchair"]; ok {
		t.Fatal("wheelchair should be parsed as metadata, not a generic tag")
	}
}

func TestParseSearchParams_RejectsInvalidSpatialInput(t *testing.T) {
	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
	}

	tests := []string{
		"/search?lat=abc&lon=9.5&radius=1km",
		"/search?lat=47.1&lon=9.5&radius=bogus",
		"/search?lat=47.1&lon=9.5",
		"/search?lat=47.1&radius=1km",
		"/search?min_lat=47.1",
		"/search?min_lat=48&max_lat=47&min_lon=9&max_lon=10",
		"/search?limit=0",
		"/search?from=-1",
	}

	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			if _, err := parseSearchParamsWithError(req, conf); err == nil {
				t.Fatalf("expected invalid spatial input to fail: %s", target)
			}
		})
	}
}

func TestParseSearchParams_NearQueryMaySetOnlyRadius(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/search?q=pizza+near+Vaduz&radius=100m", nil)
	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
	}

	params, err := parseSearchParamsWithError(req, conf)
	if err != nil {
		t.Fatalf("expected near query with radius to parse: %v", err)
	}
	if params.Radius != "100m" {
		t.Fatalf("expected radius to be preserved, got %q", params.Radius)
	}
}

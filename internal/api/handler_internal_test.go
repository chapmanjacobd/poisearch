package api

import (
	"net/http/httptest"
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/config"
)

func TestParseSearchParams_ExactMatch(t *testing.T) {
	req := httptest.NewRequest("GET", "/search?q=park&exact_match=true", nil)
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
	req := httptest.NewRequest("GET", "/search?street=Main+St+123", nil)
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
	req := httptest.NewRequest("GET", "/search?street=123+Main+St", nil)
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
	req := httptest.NewRequest("GET", "/search?street=Main+St+123&housenumber=123", nil)
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
	req := httptest.NewRequest("GET", "/search?street=123+Main+St&housenumber=123", nil)
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

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

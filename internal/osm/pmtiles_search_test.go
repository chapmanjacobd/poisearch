package osm_test

import (
	"os"
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

func TestPMTilesSearch(t *testing.T) {
	pmtilesPath := "../../naturalearth-openmaptiles.2025-12-10.full.pmtiles"
	if _, err := os.Stat(pmtilesPath); err != nil {
		t.Skipf("PMTiles file not found at %s, skipping test", pmtilesPath)
	}

	conf := &config.Config{
		Importance: config.ImportanceWeights{
			Place:   map[string]float64{"city": 4.0, "country": 5.0},
			Default: 1.0,
		},
	}

	// Vaduz
	lat := 47.14
	lon := 9.52

	t.Run("QueryAndRadiusSearch", func(t *testing.T) {
		params := search.SearchParams{
			Query:  "Vaduz",
			Lat:    &lat,
			Lon:    &lon,
			Radius: "10km",
			Limit:  10,
		}

		res, err := osm.PMTilesSearch(pmtilesPath, params, conf)
		if err != nil {
			t.Fatalf("PMTilesSearch failed: %v", err)
		}

		if res.Total == 0 {
			t.Errorf("expected at least 1 result for Vaduz, got 0")
		}

		for _, hit := range res.Hits {
			t.Logf("Found: %s (Score: %f)", hit.Fields["name"], hit.Score)
		}
	})

	t.Run("RadiusSearchNoQuery", func(t *testing.T) {
		params := search.SearchParams{
			Lat:    &lat,
			Lon:    &lon,
			Radius: "1000m",
			Limit:  10,
		}
		res, err := osm.PMTilesSearch(pmtilesPath, params, conf)
		if err != nil {
			t.Fatalf("PMTilesSearch failed: %v", err)
		}
		if res.Total == 0 {
			t.Errorf("expected results for radius search around Vaduz, got 0")
		}
		t.Logf("Radius search found %d results", res.Total)
	})

	t.Run("BBoxSearch", func(t *testing.T) {
		minLat, maxLat := 47.0, 48.0
		minLon, maxLon := 9.0, 10.0
		params := search.SearchParams{
			MinLat: &minLat,
			MaxLat: &maxLat,
			MinLon: &minLon,
			MaxLon: &maxLon,
			Limit:  10,
		}
		res, err := osm.PMTilesSearch(pmtilesPath, params, conf)
		if err != nil {
			t.Fatalf("PMTilesSearch failed: %v", err)
		}
		if res.Total == 0 {
			t.Errorf("expected results for bbox search, got 0")
		}
		t.Logf("BBox search found %d results", res.Total)
	})
}

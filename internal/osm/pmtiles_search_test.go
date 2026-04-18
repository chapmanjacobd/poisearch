package osm_test

import (
	"os"
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

func TestPMTilesSearch(t *testing.T) {
	pmtilesPath := "../../liechtenstein.pmtiles"
	if _, err := os.Stat(pmtilesPath); err != nil {
		t.Fatalf("PMTiles file not found at %s. Run scripts/pbf_to_pmtiles.sh to generate it.", pmtilesPath)
	}

	conf := &config.Config{
		StoreSecondaryNames: true,
		Importance: config.ImportanceWeights{
			Boosts:  []string{"city", "country", "town", "village"},
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
			t.Errorf("expected at least 1 result, got 0")
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

	t.Run("ExactMatchPreciseFiltering", func(t *testing.T) {
		params := search.SearchParams{
			Query:      "Vaduz",
			Lat:        &lat,
			Lon:        &lon,
			Radius:     "500m",
			Limit:      10,
			ExactMatch: true,
		}
		res, err := osm.PMTilesSearch(pmtilesPath, params, conf)
		if err != nil {
			t.Fatalf("PMTilesSearch failed: %v", err)
		}
		if res.Total == 0 {
			t.Errorf("expected at least 1 result, got 0")
		}
		t.Logf("ExactMatch search found %d results", res.Total)
	})

	t.Run("AddressSearch", func(t *testing.T) {
		// From debug, we know there are housenumbers in liechtenstein.pmtiles
		params := search.SearchParams{
			Lat:         &lat,
			Lon:         &lon,
			Radius:      "10km",
			HouseNumber: "1",
			Limit:       10,
		}

		res, err := osm.PMTilesSearch(pmtilesPath, params, conf)
		if err != nil {
			t.Fatalf("PMTilesSearch failed: %v", err)
		}
		if res.Total == 0 {
			t.Errorf("expected results for address search (housenumber=1), got 0")
		}

		for _, hit := range res.Hits {
			hn, _ := hit.Fields["addr:housenumber"].(string)
			if hn != "1" {
				t.Errorf("expected housenumber 1, got %s", hn)
			}
			t.Logf("Found address: %s %s", hn, hit.Fields["addr:street"])
		}
	})

	t.Run("CitySearch", func(t *testing.T) {
		params := search.SearchParams{
			Lat:    &lat,
			Lon:    &lon,
			Radius: "10km",
			City:   "Vaduz",
			Limit:  10,
		}

		res, err := osm.PMTilesSearch(pmtilesPath, params, conf)
		if err != nil {
			t.Fatalf("PMTilesSearch failed: %v", err)
		}
		if res.Total == 0 {
			t.Errorf("expected at least 1 result, got 0")
		}

		// Even if addr:city is not ubiquitous, we should find something in a well-tagged area
		t.Logf("City search (Vaduz) found %d results", res.Total)
		for _, hit := range res.Hits {
			t.Logf("Found: %s", hit.Fields["name"])
		}
	})

	t.Run("GlobalSearch", func(t *testing.T) {
		params := search.SearchParams{
			Query: "Vaduz",
			Limit: 5,
		}

		res, err := osm.PMTilesSearch(pmtilesPath, params, conf)
		if err != nil {
			t.Fatalf("PMTilesSearch failed: %v", err)
		}
		if res.Total == 0 {
			t.Errorf("expected global search to find Vaduz, got 0")
		}
		t.Logf("Global search found %d results", res.Total)
	})
}

package osm

import (
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/search"
)

func TestMatchesCoordsSpatialFilter_RadiusUsesWholeLine(t *testing.T) {
	lat := 60.0
	lon := 0.0
	params := search.SearchParams{
		Lat:    &lat,
		Lon:    &lon,
		Radius: "600m",
	}

	filter := computeSpatialFilter(params)
	coords := [][]float64{
		{-0.02, 60.0},
		{0.02, 60.0},
	}

	if matchesCoordSpatialFilter(coords[0], &filter, params, filter.radiusMeters) {
		t.Fatal("expected first coordinate to be outside the radius filter")
	}

	if !matchesCoordsSpatialFilter(coords, &filter, params, filter.radiusMeters) {
		t.Fatal("expected full line geometry to match radius filter")
	}
}

func TestMatchesCoordsSpatialFilter_BBoxUsesGeometryBounds(t *testing.T) {
	minLat, maxLat := 59.999, 60.001
	minLon, maxLon := -0.005, 0.005
	params := search.SearchParams{
		MinLat: &minLat,
		MaxLat: &maxLat,
		MinLon: &minLon,
		MaxLon: &maxLon,
	}

	filter := computeSpatialFilter(params)
	coords := [][]float64{
		{-0.02, 60.0},
		{0.02, 60.0},
	}

	if matchesCoordSpatialFilter(coords[0], &filter, params, filter.radiusMeters) {
		t.Fatal("expected first coordinate to be outside the bbox filter")
	}

	if !matchesCoordsSpatialFilter(coords, &filter, params, filter.radiusMeters) {
		t.Fatal("expected line bounds to intersect bbox filter")
	}
}

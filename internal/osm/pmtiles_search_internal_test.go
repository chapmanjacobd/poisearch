package osm

import (
	"math"
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/search"
	"github.com/paulmach/orb"
)

func TestMatchesGeometryCoarseFilter_MultiPolygonUsesBounds(t *testing.T) {
	lat := 60.0
	lon := 0.0
	params := search.SearchParams{
		Lat:    &lat,
		Lon:    &lon,
		Radius: "1500m",
	}

	filter := computeSpatialFilter(params)
	radiusMeters := parseRadiusToInt(params.Radius)

	geom := orb.MultiPolygon{
		{
			{
				{0.05, 60.0},
				{0.051, 60.0},
				{0.051, 60.001},
				{0.05, 60.001},
				{0.05, 60.0},
			},
		},
		{
			{
				{0.009, 60.0},
				{0.010, 60.0},
				{0.010, 60.001},
				{0.009, 60.001},
				{0.009, 60.0},
			},
		},
	}

	coords := featureToCoords(geom)
	if len(coords) == 0 {
		t.Fatal("expected representative coordinates for multipolygon")
	}
	if matchesCoordSpatialFilter(coords[0], &filter, params, radiusMeters) {
		t.Fatal("expected first polygon to be outside the coarse radius filter")
	}
	if !matchesGeometryCoarseFilter(geom, coords, &filter, params, radiusMeters) {
		t.Fatal("expected multipolygon bounds to keep the nearby secondary polygon")
	}
}

func TestMatchesPreciseFilter_RadiusStaysWithinTolerance(t *testing.T) {
	lat := 60.0
	lon := 0.0
	params := search.SearchParams{
		Lat: &lat,
		Lon: &lon,
	}

	geom := orb.LineString{
		{0.009, 60.0},
		{0.010, 60.0},
	}

	exact := float64(computeDistanceMeters(lat, lon, lat, 0.009))
	approx := localMetricDistanceFrom(geom, orb.Point{lon, lat})
	if diff := math.Abs(approx - exact); diff > 30 {
		t.Fatalf("local metric distance diff = %.2f meters, want <= 30", diff)
	}

	if !matchesPreciseFilter(geom, &spatialFilter{hasRadius: true}, params, 600) {
		t.Fatal("expected radius filter to keep geometry within 600 meters")
	}
}

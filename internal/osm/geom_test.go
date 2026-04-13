package osm

import (
	"testing"

	"github.com/twpayne/go-geos"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    float64
		expected float64
	}{
		{12.3456789, 12.34568},
		{12.3456744, 12.34567},
		{-12.3456789, -12.34568},
		{0.0, 0.0},
	}

	for _, tt := range tests {
		got := truncate(tt.input)
		if got != tt.expected {
			t.Errorf("truncate(%f) = %f; want %f", tt.input, got, tt.expected)
		}
	}
}

func TestRepresentativePoint(t *testing.T) {
	ctx := geos.NewContext()
	
	// Test Polygon
	polyCoords := [][][]float64{{{0, 0}, {0, 10}, {10, 10}, {10, 0}, {0, 0}}}
	poly := ctx.NewPolygon(polyCoords)
	pt, isPt, err := representativePoint(poly, ctx)
	if err != nil {
		t.Fatalf("representativePoint failed: %v", err)
	}
	if isPt {
		t.Error("expected isPt to be false for polygon")
	}
	if pt.X() != 5 || pt.Y() != 5 {
		t.Errorf("expected centroid at (5,5), got (%f,%f)", pt.X(), pt.Y())
	}

	// Test LineString
	lineCoords := [][]float64{{0, 0}, {10, 0}}
	line := ctx.NewLineString(lineCoords)
	pt, _, _ = representativePoint(line, ctx)
	if pt.X() != 5 || pt.Y() != 0 {
		t.Errorf("expected midpoint at (5,0), got (%f,%f)", pt.X(), pt.Y())
	}
}

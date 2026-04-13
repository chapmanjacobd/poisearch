package osm

import (
	"fmt"
	"math"

	"github.com/paulmach/osm"
	"github.com/twpayne/go-geos"
)

type GeometryMode string

const (
	ModeGeopoint         GeometryMode = "geopoint"
	ModeGeoshapeBBox     GeometryMode = "geoshape-bbox"
	ModeGeoshapeSimplify GeometryMode = "geoshape-simplified"
	ModeGeoshapeFull     GeometryMode = "geoshape-full"
	ModeNoGeo            GeometryMode = "no-geo"
)

func CreateGeometry(obj osm.Object, mode GeometryMode, tolerance float64, ctx *geos.Context) (any, error) {
	var g *geos.Geom

	switch o := obj.(type) {
	case *osm.Node:
		g = ctx.NewPointFromXY(o.Lon, o.Lat)
	case *osm.Way:
		coords := make([][]float64, len(o.Nodes))
		for i, n := range o.Nodes {
			coords[i] = []float64{n.Lon, n.Lat}
		}
		if len(coords) < 2 {
			return nil, fmt.Errorf("way with fewer than 2 nodes")
		}
		// If it's a closed way, it might be a polygon.
		if len(coords) >= 4 && coords[0][0] == coords[len(coords)-1][0] && coords[0][1] == coords[len(coords)-1][1] {
			g = ctx.NewPolygon([][][]float64{coords})
		} else {
			g = ctx.NewLineString(coords)
		}
	case *osm.Relation:
		// Many PBF exporters (like osmium) can include the bounding box in the header.
		// For relations, this is the most lightweight way to get a geometry.
		if o.Bounds != nil {
			g = ctx.NewGeomFromBounds(o.Bounds.MinLon, o.Bounds.MinLat, o.Bounds.MaxLon, o.Bounds.MaxLat)
		} else {
			return nil, fmt.Errorf("relation without bounds")
		}
	default:
		// Relations if needed, but for "lightweight" we might skip them or just use Centroid if pre-processed.
		return nil, fmt.Errorf("unsupported object type")
	}

	if g == nil {
		return nil, fmt.Errorf("could not create geometry")
	}

	if mode == ModeGeopoint {
		pt, _, err := representativePoint(g, ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"lat": truncate(pt.Y()),
			"lon": truncate(pt.X()),
		}, nil
	}

	// For geoshape modes
	var finalGeom *geos.Geom
	switch mode {
	case ModeGeoshapeBBox:
		finalGeom = g.Envelope()
	case ModeGeoshapeSimplify:
		finalGeom = g.TopologyPreserveSimplify(tolerance)
	case ModeGeoshapeFull:
		finalGeom = g
	default:
		// Default to Geopoint if none specified in config or invalid
		pt, _, err := representativePoint(g, ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"lat": truncate(pt.Y()),
			"lon": truncate(pt.X()),
		}, nil
	}

	// Convert finalGeom to GeoJSON-like structure for Bleve
	return geomToGeoJSON(finalGeom), nil
}

func truncate(f float64) float64 {
	shift := math.Pow(10, 5)
	return math.Floor(f*shift+0.5) / shift
}

func representativePoint(g *geos.Geom, ctx *geos.Context) (*geos.Geom, bool, error) {
	switch g.TypeID() {
	case geos.TypeIDPoint:
		return g, true, nil
	case geos.TypeIDLineString, geos.TypeIDMultiLineString:
		pt := g.InterpolateNormalized(0.5)
		return pt, false, nil
	case geos.TypeIDPolygon, geos.TypeIDMultiPolygon:
		centroid := g.Centroid()
		if g.Intersects(centroid) {
			return centroid, false, nil
		}
		return g.PointOnSurface(), false, nil
	default:
		return g.PointOnSurface(), false, nil
	}
}

func geomToGeoJSON(g *geos.Geom) map[string]any {
	// Minimal GeoJSON conversion for Bleve
	// Bleve specifically mentions "coordinates" and "type"
	switch g.TypeID() {
	case geos.TypeIDPoint:
		return map[string]any{
			"type":        "point",
			"coordinates": []float64{truncate(g.X()), truncate(g.Y())},
		}
	case geos.TypeIDLineString:
		coords := g.CoordSeq().ToCoords()
		for i := range coords {
			coords[i][0] = truncate(coords[i][0])
			coords[i][1] = truncate(coords[i][1])
		}
		return map[string]any{
			"type":        "linestring",
			"coordinates": coords,
		}
	case geos.TypeIDPolygon:
		// Simplified: only outer ring
		ring := g.ExteriorRing()
		coords := ring.CoordSeq().ToCoords()
		for i := range coords {
			coords[i][0] = truncate(coords[i][0])
			coords[i][1] = truncate(coords[i][1])
		}
		return map[string]any{
			"type":        "polygon",
			"coordinates": [][][]float64{coords},
		}
	default:
		// Fallback to centroid for unsupported complex types to keep it lightweight
		centroid := g.Centroid()
		return map[string]any{
			"type":        "point",
			"coordinates": []float64{truncate(centroid.X()), truncate(centroid.Y())},
		}
	}
}

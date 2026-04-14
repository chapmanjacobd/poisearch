package osm

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/blevesearch/bleve/v2"
	bleveSearch "github.com/blevesearch/bleve/v2/search"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/protomaps/go-pmtiles/pmtiles"
	"github.com/twpayne/go-geos"
)

// PMTilesSearch performs a search directly on a PMTiles archive.
// It is fast for spatial queries (radius/bbox) as it only reads intersecting tiles.
func PMTilesSearch(pmtilesPath string, params search.SearchParams, conf *config.Config) (*bleve.SearchResult, error) {
	// Load ontology for classification
	ont := DefaultOntology()
	if conf.OntologyPath != "" {
		if loaded, err := LoadOntologyFromCSV(conf.OntologyPath); err == nil {
			ont = loaded
		}
	}

	file, err := os.Open(pmtilesPath)
	if err != nil {
		return nil, fmt.Errorf("opening PMTiles file: %w", err)
	}
	defer file.Close()

	reader, err := pmtiles.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("creating PMTiles reader: %w", err)
	}

	header := reader.Header()
	maxZoom := int(header.MaxZoom)

	res := &bleve.SearchResult{
		Hits: make(bleveSearch.DocumentMatchCollection, 0),
	}

	geosCtx := geos.NewContext()
	queryLower := strings.ToLower(params.Query)

	filter := computeSpatialFilter(params)
	if !filter.hasRadius && !filter.hasBbox {
		// If no spatial filter, PMTiles search is not ideal but we can't easily scan everything efficiently.
		// For now, return an error or empty result to avoid accidental full-archive scans.
		return nil, fmt.Errorf("PMTiles search requires a spatial filter (radius or bbox)")
	}

	// Compute tiles at MaxZoom that intersect the search area
	minTileX, minTileY, maxTileX, maxTileY := getTileRange(filter, maxZoom)

	// Fetch tiles and search features
	for x := minTileX; x <= maxTileX; x++ {
		for y := minTileY; y <= maxTileY; y++ {
			err := processTile(reader, uint8(maxZoom), uint32(x), uint32(y), res, params, conf, ont, geosCtx, queryLower)
			if err != nil {
				// Log error but continue with other tiles
				fmt.Fprintf(os.Stderr, "Error processing tile %d/%d/%d: %v\n", maxZoom, x, y, err)
				continue
			}
			if params.Limit > 0 && len(res.Hits) >= params.Limit {
				return res, nil
			}
		}
	}

	return res, nil
}

func getTileRange(f spatialFilter, zoom int) (minX, minY, maxX, maxY int) {
	minLat := float64(f.minLat) / 1_000_000_000
	maxLat := float64(f.maxLat) / 1_000_000_000
	minLon := float64(f.minLon) / 1_000_000_000
	maxLon := float64(f.maxLon) / 1_000_000_000

	minX, minY = lonLatToTile(minLon, maxLat, zoom) // Note: maxY is lower latitude in tile coordinates
	maxX, maxY = lonLatToTile(maxLon, minLat, zoom)

	if minX > maxX {
		minX, maxX = maxX, minX
	}
	if minY > maxY {
		minY, maxY = maxY, minY
	}

	return
}

func lonLatToTile(lon, lat float64, zoom int) (int, int) {
	n := math.Pow(2, float64(zoom))
	x := int((lon + 180.0) / 360.0 * n)
	y := int((1.0 - math.Log(math.Tan(lat*math.Pi/180.0)+1.0/math.Cos(lat*math.Pi/180.0))/math.Pi) / 2.0 * n)
	return x, y
}

func processTile(
	reader *pmtiles.Reader,
	z uint8, x, y uint32,
	res *bleve.SearchResult,
	params search.SearchParams,
	conf *config.Config,
	ont *PlaceTypeOntology,
	geosCtx *geos.Context,
	queryLower string,
) error {
	tileData, err := reader.GetTile(context.Background(), z, x, y)
	if err != nil {
		if err == io.EOF {
			return nil // Tile doesn't exist in archive
		}
		return err
	}
	if tileData == nil {
		return nil
	}

	// Decompress if needed (MVTs in PMTiles are usually gzipped)
	var r io.Reader = bytes.NewReader(tileData)
	if len(tileData) > 2 && tileData[0] == 0x1f && tileData[1] == 0x8b {
		gr, err := gzip.NewReader(r)
		if err != nil {
			return err
		}
		defer gr.Close()
		r = gr
	}

	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	layers, err := mvt.Unmarshal(body)
	if err != nil {
		return err
	}

	// Calculate tile boundaries for coordinate projection
	tileBBox := tileToBBox(int(x), int(y), int(z))

	for _, layer := range layers {
		// Only search layers likely to contain POIs
		if !isPOILayer(layer.Name) {
			continue
		}

		for _, feature := range layer.Features {
			tags := make(map[string]string)
			for k, v := range feature.Properties {
				tags[k] = fmt.Sprint(v)
			}

			// Normalize some MVT-specific tags to OSM tags if needed
			if _, ok := tags["name"]; !ok && tags["name:en"] != "" {
				tags["name"] = tags["name:en"]
			}

			// Convert geometry to WGS84 coords
			coords := featureToCoords(feature, layer.Extent, tileBBox)
			if len(coords) == 0 {
				continue
			}

			latNano := nanodegree(coords[0][1])
			lonNano := nanodegree(coords[0][0])

			// Reuse the spatial filter check from PBF search
			filter := computeSpatialFilter(params)
			radiusMeters := parseRadiusToInt(params.Radius)
			if !matchesSpatialFilter(latNano, lonNano, filter.hasRadius, filter.hasBbox,
				filter.minLat, filter.maxLat, filter.minLon, filter.maxLon, params, radiusMeters) {
				continue
			}

			if hit := processPBFFntity("pmtiles", int64(feature.ID), tags, coords,
				queryLower, params, conf, ont, geosCtx); hit != nil {
				if collectHit(res, hit, params) {
					return nil // Limit reached
				}
			}
		}
	}

	return nil
}

func isPOILayer(name string) bool {
	switch name {
	case "pois", "place", "places", "transportation", "water", "landuse", "point":
		return true
	}
	return false
}

func featureToCoords(f *mvt.Feature, extent uint32, tileBBox orb.Bound) [][]float64 {
	// This is a simplified unprojection. For more accuracy, use orb's projection helpers.
	// We'll treat all geometries as points for simplicity in this initial implementation,
	// or unproject each point if it's a line/polygon.
	
	// Get the center or the first point for now
	var pts []orb.Point
	switch g := f.Geometry.(type) {
	case orb.Point:
		pts = []orb.Point{g}
	case orb.MultiPoint:
		pts = g
	case orb.LineString:
		pts = g
	case orb.MultiLineString:
		if len(g) > 0 {
			pts = g[0]
		}
	case orb.Polygon:
		if len(g) > 0 {
			pts = g[0]
		}
	case orb.MultiPolygon:
		if len(g) > 0 && len(g[0]) > 0 {
			pts = g[0][0]
		}
	}

	if len(pts) == 0 {
		return nil
	}

	res := make([][]float64, 0, len(pts))
	for _, p := range pts {
		lon := tileBBox.Min[0] + (p[0]/float64(extent))*(tileBBox.Max[0]-tileBBox.Min[0])
		// Mercator Y is not linear, but for small tiles it's close enough? 
		// Actually, we should use a proper Mercator unprojection.
		lat := yToLat(tileBBox.Min[1] + (1.0 - p[1]/float64(extent))*(tileBBox.Max[1]-tileBBox.Min[1]))
		res = append(res, []float64{lon, lat})
	}
	return res
}

func tileToBBox(x, y, z int) orb.Bound {
	n := math.Pow(2, float64(z))
	minLon := float64(x)/n*360.0 - 180.0
	maxLon := float64(x+1)/n*360.0 - 180.0
	
	// These are in "Web Mercator" space [0, 1]
	minY := float64(y) / n
	maxY := float64(y+1) / n
	
	return orb.Bound{Min: orb.Point{minLon, minY}, Max: orb.Point{maxLon, maxY}}
}

func yToLat(y float64) float64 {
	return math.Atan(math.Sinh(math.Pi*(1.0-2.0*y))) * 180.0 / math.Pi
}

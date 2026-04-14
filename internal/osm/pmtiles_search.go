package osm

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blevesearch/bleve/v2"
	bleveSearch "github.com/blevesearch/bleve/v2/search"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/maptile"
	"github.com/protomaps/go-pmtiles/pmtiles"
	"github.com/twpayne/go-geos"
)

var (
	pmtilesServers = make(map[string]*pmtiles.Server)
	pmtilesMu      sync.Mutex
)

func getOrCreateServer(pmtilesPath string) (*pmtiles.Server, string, error) {
	pmtilesMu.Lock()
	defer pmtilesMu.Unlock()

	if s, ok := pmtilesServers[pmtilesPath]; ok {
		return s, filepath.Base(pmtilesPath), nil
	}

	dir := filepath.Dir(pmtilesPath)
	filename := filepath.Base(pmtilesPath)
	bucket := pmtiles.NewFileBucket(dir)

	// Create a PMTiles server
	server, err := pmtiles.NewServerWithBucket(bucket, "", log.Default(), 1000, "")
	if err != nil {
		return nil, "", err
	}

	pmtilesServers[pmtilesPath] = server
	return server, filename, nil
}

// PMTilesSearch performs a search directly on a PMTiles archive.
// It is fast for spatial queries (radius/bbox) as it only reads intersecting tiles.
func PMTilesSearch(pmtilesPath string, params search.SearchParams, conf *config.Config) (*bleve.SearchResult, error) {
	ctx := context.Background()

	// Load ontology for classification
	ont := DefaultOntology()
	if conf.OntologyPath != "" {
		if loaded, err := LoadOntologyFromCSV(conf.OntologyPath); err == nil {
			ont = loaded
		}
	}

	server, filename, err := getOrCreateServer(pmtilesPath)
	if err != nil {
		return nil, fmt.Errorf("getting PMTiles server: %w", err)
	}

	// Read header once to get max zoom
	bucket := pmtiles.NewFileBucket(filepath.Dir(pmtilesPath))
	defer bucket.Close()

	r, err := bucket.NewRangeReader(ctx, filename, 0, pmtiles.HeaderV3LenBytes)
	if err != nil {
		return nil, fmt.Errorf("reading PMTiles header: %w", err)
	}
	headerBytes, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		return nil, fmt.Errorf("reading PMTiles header bytes: %w", err)
	}

	header, err := pmtiles.DeserializeHeader(headerBytes)
	if err != nil {
		return nil, fmt.Errorf("deserializing PMTiles header: %w", err)
	}
	maxZoom := int(header.MaxZoom)

	res := &bleve.SearchResult{
		Hits: make(bleveSearch.DocumentMatchCollection, 0),
	}

	geosCtx := geos.NewContext()
	queryLower := strings.ToLower(params.Query)

	filter := computeSpatialFilter(params)
	if !filter.hasRadius && !filter.hasBbox {
		return nil, fmt.Errorf("PMTiles search requires a spatial filter (radius or bbox)")
	}

	// Compute tiles at MaxZoom that intersect the search area
	minTileX, minTileY, maxTileX, maxTileY := getTileRange(filter, maxZoom)

	// Fetch tiles and search features
	for x := minTileX; x <= maxTileX; x++ {
		for y := minTileY; y <= maxTileY; y++ {
			err := processTile(
				ctx,
				server,
				filename,
				uint32(maxZoom),
				uint32(x),
				uint32(y),
				res,
				params,
				conf,
				ont,
				geosCtx,
				queryLower,
			)
			if err != nil {
				// Continue with other tiles
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

	minX, minY = lonLatToTile(minLon, maxLat, zoom)
	maxX, maxY = lonLatToTile(maxLon, minLat, zoom)

	if minX > maxX {
		minX, maxX = maxX, minX
	}
	if minY > maxY {
		minY, maxY = maxY, minY
	}

	return minX, minY, maxX, maxY
}

func lonLatToTile(lon, lat float64, zoom int) (int, int) {
	n := math.Pow(2, float64(zoom))
	x := int((lon + 180.0) / 360.0 * n)
	y := int((1.0 - math.Log(math.Tan(lat*math.Pi/180.0)+1.0/math.Cos(lat*math.Pi/180.0))/math.Pi) / 2.0 * n)
	return x, y
}

func processTile(
	ctx context.Context,
	server *pmtiles.Server,
	filename string,
	z, x, y uint32,
	res *bleve.SearchResult,
	params search.SearchParams,
	conf *config.Config,
	ont *PlaceTypeOntology,
	geosCtx *geos.Context,
	queryLower string,
) error {
	// Remove .pmtiles extension if it's there
	id := strings.TrimSuffix(filename, ".pmtiles")
	path := fmt.Sprintf("/%s/%d/%d/%d.mvt", id, z, x, y)
	status, _, tileData := server.Get(ctx, path)
	if status != 200 || len(tileData) == 0 {
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

	tile := maptile.New(x, y, maptile.Zoom(z))

	for _, layer := range layers {
		if !isPOILayer(layer.Name) {
			continue
		}

		// Project all features in the layer to WGS84
		layer.ProjectToWGS84(tile)

		for _, feature := range layer.Features {
			tags := make(map[string]string)
			for k, v := range feature.Properties {
				tags[k] = fmt.Sprint(v)
			}

			// Normalize MVT tags to OSM tags
			if _, ok := tags["name"]; !ok && tags["name:en"] != "" {
				tags["name"] = tags["name:en"]
			}

			coords := featureToCoords(feature.Geometry)
			if len(coords) == 0 {
				continue
			}

			latNano := nanodegree(coords[0][1])
			lonNano := nanodegree(coords[0][0])

			filter := computeSpatialFilter(params)
			radiusMeters := parseRadiusToInt(params.Radius)
			if !matchesSpatialFilter(latNano, lonNano, filter.hasRadius, filter.hasBbox,
				filter.minLat, filter.maxLat, filter.minLon, filter.maxLon, params, radiusMeters) {
				continue
			}

			id := int64(0)
			if feature.ID != nil {
				switch v := feature.ID.(type) {
				case int64:
					id = v
				case uint64:
					id = int64(v)
				case float64:
					id = int64(v)
				case int:
					id = int64(v)
				}
			}

			if hit := processPBFFntity("pmtiles", id, tags, coords,
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

func featureToCoords(g orb.Geometry) [][]float64 {
	var pts []orb.Point
	switch geom := g.(type) {
	case orb.Point:
		pts = []orb.Point{geom}
	case orb.MultiPoint:
		pts = geom
	case orb.LineString:
		pts = geom
	case orb.MultiLineString:
		if len(geom) > 0 {
			pts = geom[0]
		}
	case orb.Polygon:
		if len(geom) > 0 {
			pts = geom[0]
		}
	case orb.MultiPolygon:
		if len(geom) > 0 && len(geom[0]) > 0 {
			pts = geom[0][0]
		}
	}

	if len(pts) == 0 {
		return nil
	}

	res := make([][]float64, 0, len(pts))
	for _, p := range pts {
		res = append(res, []float64{p.X(), p.Y()})
	}
	return res
}

package osm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
	"github.com/paulmach/orb/planar"
	"github.com/protomaps/go-pmtiles/pmtiles"
	"github.com/twpayne/go-geos"
)

// pmtilesCache stores bucket and header info for opened PMTiles files
type pmtilesCache struct {
	bucket   *pmtiles.FileBucket
	filename string
	header   pmtiles.HeaderV3
}

type pmtilesManager struct {
	archives map[string]*pmtilesCache
	mu       sync.Mutex
}

//nolint:gochecknoglobals // global manager used for caching opened PMTiles archives
var globalPMTilesManager = &pmtilesManager{
	archives: make(map[string]*pmtilesCache),
}

// getOrCreateArchive opens or retrieves a cached PMTiles archive.
func (m *pmtilesManager) getOrCreateArchive(pmtilesPath string) (*pmtilesCache, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cache, ok := m.archives[pmtilesPath]; ok {
		return cache, nil
	}

	ctx := context.Background()
	dir := filepath.Dir(pmtilesPath)
	filename := filepath.Base(pmtilesPath)
	bucket := pmtiles.NewFileBucket(dir)

	// Read header
	r, err := bucket.NewRangeReader(ctx, filename, 0, pmtiles.HeaderV3LenBytes)
	if err != nil {
		_ = bucket.Close()
		return nil, fmt.Errorf("reading PMTiles header: %w", err)
	}
	defer r.Close()

	headerBytes, err := io.ReadAll(r)
	if err != nil {
		_ = bucket.Close()
		return nil, fmt.Errorf("reading PMTiles header bytes: %w", err)
	}

	header, err := pmtiles.DeserializeHeader(headerBytes)
	if err != nil {
		_ = bucket.Close()
		return nil, fmt.Errorf("deserializing PMTiles header: %w", err)
	}

	cache := &pmtilesCache{
		bucket:   bucket,
		filename: filename,
		header:   header,
	}
	m.archives[pmtilesPath] = cache
	return cache, nil
}

// PMTilesSearch performs a search directly on a PMTiles archive.
// It reads tiles directly without the overhead of a PMTiles server.
func PMTilesSearch(pmtilesPath string, params search.SearchParams, conf *config.Config) (*bleve.SearchResult, error) {
	ctx := context.Background()

	// Load ontology for classification
	ont := DefaultOntology()
	if conf.OntologyPath != "" {
		if loaded, err := LoadOntologyFromCSV(conf.OntologyPath); err == nil {
			ont = loaded
		}
	}

	archive, err := globalPMTilesManager.getOrCreateArchive(pmtilesPath)
	if err != nil {
		return nil, fmt.Errorf("opening PMTiles archive: %w", err)
	}

	res := &bleve.SearchResult{
		Hits: make(bleveSearch.DocumentMatchCollection, 0),
	}

	geosCtx := geos.NewContext()
	queryLower := strings.ToLower(params.Query)

	filter := computeSpatialFilter(params)
	if !filter.hasRadius && !filter.hasBbox {
		return nil, errors.New("PMTiles search requires a spatial filter (radius or bbox)")
	}

	// Use the archive's max zoom level
	maxZoom := int(archive.header.MaxZoom)

	// Compute tiles at MaxZoom that intersect the search area
	minTileX, minTileY, maxTileX, maxTileY := getTileRange(filter, maxZoom)

	pOpts := &processTileOptions{
		archive:    archive,
		res:        res,
		params:     params,
		conf:       conf,
		ont:        ont,
		geosCtx:    geosCtx,
		queryLower: queryLower,
	}

	// Fetch tiles and search features
	for x := minTileX; x <= maxTileX; x++ {
		for y := minTileY; y <= maxTileY; y++ {
			pOpts.z = uint32(maxZoom)
			pOpts.x = uint32(x)
			pOpts.y = uint32(y)
			err := processTile(ctx, pOpts)
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

func lonLatToTile(lon, lat float64, zoom int) (x, y int) {
	n := math.Pow(2, float64(zoom))
	x = int((lon + 180.0) / 360.0 * n)
	y = int((1.0 - math.Log(math.Tan(lat*math.Pi/180.0)+1.0/math.Cos(lat*math.Pi/180.0))/math.Pi) / 2.0 * n)
	return x, y
}

type processTileOptions struct {
	archive    *pmtilesCache
	z, x, y    uint32
	res        *bleve.SearchResult
	params     search.SearchParams
	conf       *config.Config
	ont        *PlaceTypeOntology
	geosCtx    *geos.Context
	queryLower string
}

func processTile(ctx context.Context, p *processTileOptions) error {
	// Read tile directly using direct access (no server overhead)
	tileData, err := readTileDirect(ctx, p.archive, uint8(p.z), p.x, p.y)
	if err != nil {
		return err
	}
	if tileData == nil {
		return nil // Tile not found
	}

	layers, err := mvt.Unmarshal(tileData)
	if err != nil {
		// Try gzipped (some PMTiles archives use gzip compression for tiles)
		layers, err = mvt.UnmarshalGzipped(tileData)
		if err != nil {
			return err
		}
	}

	tile := maptile.New(p.x, p.y, maptile.Zoom(p.z))

	for _, layer := range layers {
		if !isPOILayer(layer.Name) {
			continue
		}

		// Project all features in the layer to WGS84
		layer.ProjectToWGS84(tile)

		for _, feature := range layer.Features {
			if done := processMVTFeature(feature, layer.Name, p); done {
				return nil
			}
		}
	}

	return nil
}

//nolint:revive // feature processing is always complex
func processMVTFeature(feature *geojson.Feature, layerName string, p *processTileOptions) bool {
	tags := make(map[string]string)
	for k, v := range feature.Properties {
		tags[k] = fmt.Sprint(v)
	}

	NormalizeNameTag(tags, p.conf.Languages)
	enhanceName(tags)

	// Map OpenMapTiles 'class' to OSM-style tags for classification
	if class, ok := tags["class"]; ok {
		switch layerName {
		case "place", "places":
			tags["place"] = class
		case "pois", "poi", "point":
			if _, ok := tags["amenity"]; !ok {
				tags["amenity"] = class
			}
			// Map to other common keys too just in case
			if _, ok := tags["shop"]; !ok {
				tags["shop"] = class
			}
			if _, ok := tags["tourism"]; !ok {
				tags["tourism"] = class
			}
		case "transportation":
			tags["highway"] = class
		case "water", "waterway":
			if _, ok := tags["natural"]; !ok {
				tags["natural"] = "water"
			}
			tags["water"] = class
		case "aerodrome_label":
			tags["aeroway"] = "aerodrome"
		case "landuse":
			tags["landuse"] = class
		case "building":
			tags["building"] = "yes"
		}
	}

	// Map other common OMT fields to OSM tags for classification/metadata
	if v, ok := tags["level"]; ok {
		tags["level"] = v
	}
	if v, ok := tags["rank"]; ok && tags["importance"] == "" {
		// Use rank as a fallback for importance if missing
		tags["importance"] = v
	}

	// Address fields (some schemas include these in 'pois' or 'housenumber' layers)
	for _, k := range []string{"housenumber", "street", "city", "postcode"} {
		if v, ok := tags[k]; ok {
			tags["addr:"+k] = v
		}
	}

	// Fast path: use first coordinate for initial spatial filtering
	coords := featureToCoords(feature.Geometry)
	if len(coords) == 0 {
		return false
	}

	latNano := nanodegree(coords[0][1])
	lonNano := nanodegree(coords[0][0])

	filter := computeSpatialFilter(p.params)
	radiusMeters := parseRadiusToInt(p.params.Radius)
	if !matchesSpatialFilter(latNano, lonNano, filter.hasRadius, filter.hasBbox,
		filter.minLat, filter.maxLat, filter.minLon, filter.maxLon, p.params, radiusMeters) {

		return false
	}

	// Opt-in: precise intersection check for non-point geometries
	if p.conf.PMTilesPostProcess || p.params.ExactMatch {
		if _, ok := feature.Geometry.(orb.Point); !ok {
			if !matchesPreciseFilter(feature.Geometry, &filter, p.params, radiusMeters) {
				return false
			}
		}
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

	if hit := processPBFEntity("pmtiles", id, tags, coords,
		p.queryLower, p.params, p.conf, p.ont, p.geosCtx); hit != nil {
		if collectHit(p.res, hit, p.params) {
			return true // Limit reached
		}
	}
	return false
}

// readTileDirect reads a tile directly from the PMTiles archive.
// Based on the approach from pmtiles/show.go - avoids server overhead.
func readTileDirect(
	ctx context.Context,
	archive *pmtilesCache,
	z uint8,
	x, y uint32,
) ([]byte, error) {
	tileID := pmtiles.ZxyToID(z, x, y)
	dirOffset := archive.header.RootOffset
	dirLength := archive.header.RootLength

	for range 4 {
		b, err := readDirectory(ctx, archive, dirOffset, dirLength)
		if err != nil {
			return nil, err
		}

		directory := pmtiles.DeserializeEntries(bytes.NewBuffer(b), archive.header.InternalCompression)
		entry, ok := pmtiles.FindTile(directory, tileID)
		if !ok {
			// Tile not found
			return nil, nil
		}

		if entry.RunLength > 0 {
			// Found the tile
			return readTileData(ctx, archive, entry)
		}
		// Leaf directory - continue search
		dirOffset = archive.header.LeafDirectoryOffset + entry.Offset
		dirLength = uint64(entry.Length)
	}

	return nil, errors.New("exceeded max directory depth")
}

func readDirectory(ctx context.Context, archive *pmtilesCache, offset, length uint64) ([]byte, error) {
	r, err := archive.bucket.NewRangeReader(ctx, archive.filename, int64(offset), int64(length))
	if err != nil {
		return nil, fmt.Errorf("reading directory: %w", err)
	}
	defer r.Close()

	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading directory bytes: %w", err)
	}
	return b, nil
}

func readTileData(ctx context.Context, archive *pmtilesCache, entry pmtiles.EntryV3) ([]byte, error) {
	tileReader, err := archive.bucket.NewRangeReader(
		ctx,
		archive.filename,
		int64(archive.header.TileDataOffset+entry.Offset),
		int64(entry.Length),
	)
	if err != nil {
		return nil, fmt.Errorf("reading tile: %w", err)
	}
	defer tileReader.Close()

	tileBytes, err := io.ReadAll(tileReader)
	if err != nil {
		return nil, fmt.Errorf("reading tile bytes: %w", err)
	}
	return tileBytes, nil
}

func isPOILayer(name string) bool {
	switch name {
	case "pois",
		"poi",
		"place",
		"places",
		"transportation",
		"water",
		"landuse",
		"point",
		"building",
		"water_name",
		"waterway",
		"aerodrome_label":
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

func matchesPreciseFilter(g orb.Geometry, f *spatialFilter, params search.SearchParams, radiusMeters int) bool {
	if f.hasRadius {
		// Precise Distance check
		center := orb.Point{*params.Lon, *params.Lat}
		dist := planar.DistanceFrom(g, center)
		// Distance is in degrees approximately, convert to meters
		distMeters := dist * 111319.9
		return distMeters <= float64(radiusMeters)
	}

	if f.hasBbox {
		// Intersection with bounding box
		rect := orb.Bound{
			Min: orb.Point{*params.MinLon, *params.MinLat},
			Max: orb.Point{*params.MaxLon, *params.MaxLat},
		}
		// Intersects check using Bounding Boxes
		return rect.Intersects(g.Bound())
	}

	return true
}

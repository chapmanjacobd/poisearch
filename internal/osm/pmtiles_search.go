package osm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	if search.IsNearQuery(params.Query) {
		return directNearSearch(params, func(nearParams search.SearchParams) (*bleve.SearchResult, error) {
			return pmtilesSearchRaw(pmtilesPath, nearParams, conf)
		})
	}

	return pmtilesSearchRaw(pmtilesPath, params, conf)
}

func pmtilesSearchRaw(
	pmtilesPath string,
	params search.SearchParams,
	conf *config.Config,
) (*bleve.SearchResult, error) {
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

	if params.Limit <= 0 {
		params.Limit = 100
	}
	if params.Limit > 1000 {
		params.Limit = 1000
	}

	collectLimit := params.Limit * 3

	geosCtx := geos.NewContext()
	queryLower := strings.ToLower(params.Query)

	pOpts := &processTileOptions{
		archive:      archive,
		res:          res,
		params:       params,
		collectLimit: collectLimit,
		conf:         conf,
		ont:          ont,
		geosCtx:      geosCtx,
		queryLower:   queryLower,
	}

	filter := computeSpatialFilter(params)

	// Use the archive's max zoom level
	maxZoom := int(archive.header.MaxZoom)

	var minTileX, minTileY, maxTileX, maxTileY int
	if !filter.hasRadius && !filter.hasBbox {
		slog.Warn("performing global PMTiles search without spatial filter",
			"path", pmtilesPath, "query", params.Query)
		return processAllTiles(ctx, archive, pOpts)
	}

	// Compute tiles at MaxZoom that intersect the search area
	minTileX, minTileY, maxTileX, maxTileY = getTileRange(filter, maxZoom)

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
			if pOpts.collectLimit > 0 && len(res.Hits) >= pOpts.collectLimit {
				return sortAndTruncateDirectHits(res, params.From, params.Limit), nil
			}
		}
	}

	return sortAndTruncateDirectHits(res, params.From, params.Limit), nil
}

func processAllTiles(ctx context.Context, archive *pmtilesCache, p *processTileOptions) (*bleve.SearchResult, error) {
	// Start from root directory
	dirOffset := archive.header.RootOffset
	dirLength := archive.header.RootLength

	err := processDirectory(ctx, archive, dirOffset, dirLength, p)
	if err != nil {
		return p.res, err
	}

	return sortAndTruncateDirectHits(p.res, p.params.From, p.params.Limit), nil
}

func processDirectory(ctx context.Context, archive *pmtilesCache, offset, length uint64, p *processTileOptions) error {
	b, err := readDirectory(ctx, archive, offset, length)
	if err != nil {
		return err
	}

	directory := pmtiles.DeserializeEntries(bytes.NewBuffer(b), archive.header.InternalCompression)
	for _, entry := range directory {
		if entry.RunLength > 0 {
			// This is a tile
			z, x, y := pmtiles.IDToZxy(entry.TileID)
			p.z = uint32(z)
			p.x = x
			p.y = y

			// We only want to process tiles at or near max zoom to avoid duplicates
			// or use all tiles if it's a small archive.
			// Usually features are most granular at max zoom.
			if z == archive.header.MaxZoom {
				if err := processTileWithData(ctx, archive, entry, p); err != nil {
					slog.DebugContext(ctx, "failed to process tile", "z", z, "x", x, "y", y, "error", err)
				}
			}
		} else {
			// Leaf directory
			leafOffset := archive.header.LeafDirectoryOffset + entry.Offset
			leafLength := uint64(entry.Length)
			if err := processDirectory(ctx, archive, leafOffset, leafLength, p); err != nil {
				return err
			}
		}

		if p.collectLimit > 0 && len(p.res.Hits) >= p.collectLimit {
			return nil
		}
	}
	return nil
}

func processTileWithData(
	ctx context.Context,
	archive *pmtilesCache,
	entry pmtiles.EntryV3,
	p *processTileOptions,
) error {
	tileData, err := readTileData(ctx, archive, entry)
	if err != nil {
		return err
	}

	layers, err := mvt.Unmarshal(tileData)
	if err != nil {
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
		layer.ProjectToWGS84(tile)
		for _, feature := range layer.Features {
			if done := processMVTFeature(feature, layer.Name, p); done {
				return nil
			}
		}
	}
	return nil
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
	archive      *pmtilesCache
	z, x, y      uint32
	res          *bleve.SearchResult
	params       search.SearchParams
	collectLimit int
	conf         *config.Config
	ont          *PlaceTypeOntology
	geosCtx      *geos.Context
	queryLower   string
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

func extractOMTTags(feature *geojson.Feature, layerName string, languages []string) map[string]string {
	tags := make(map[string]string)
	for k, v := range feature.Properties {
		tags[k] = fmt.Sprint(v)
	}

	NormalizeNameTag(tags, languages)
	EnhanceName(tags)

	// Map OpenMapTiles 'class' or 'subclass' or 'key' to OSM-style tags for classification
	key := tags["class"]
	if key == "" {
		key = tags["subclass"]
	}
	if k, ok := tags["key"]; ok && key == "" {
		key = k
	}

	if key != "" {
		switch layerName {
		case "place", "places":
			tags["place"] = key
		case "pois", "poi", "point":
			if _, ok := tags["amenity"]; !ok {
				tags["amenity"] = key
			}
			// Map to other common keys too just in case
			if _, ok := tags["shop"]; !ok {
				tags["shop"] = key
			}
			if _, ok := tags["tourism"]; !ok {
				tags["tourism"] = key
			}
		case "transportation":
			tags["highway"] = key
		case "water", "waterway":
			if _, ok := tags["natural"]; !ok {
				tags["natural"] = "water"
			}
			tags["water"] = key
		case "aerodrome_label":
			tags["aeroway"] = "aerodrome"
		case "landuse":
			tags["landuse"] = key
		case "building":
			tags["building"] = "yes"
		case "housenumber":
			if _, ok := tags["amenity"]; !ok {
				tags["amenity"] = "address"
			}
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
	return tags
}

func extractFeatureID(fid any) int64 {
	if fid == nil {
		return 0
	}
	switch v := fid.(type) {
	case int64:
		return v
	case uint64:
		return int64(v)
	case float64:
		return int64(v)
	case int:
		return int64(v)
	}
	return 0
}

func processMVTFeature(feature *geojson.Feature, layerName string, p *processTileOptions) bool {
	tags := extractOMTTags(feature, layerName, p.conf.Languages)
	coords := featureToCoords(feature.Geometry)
	if len(coords) == 0 {
		return false
	}

	filter := computeSpatialFilter(p.params)
	radiusMeters := parseRadiusToInt(p.params.Radius)
	if !matchesGeometryCoarseFilter(feature.Geometry, coords, &filter, p.params, radiusMeters) {
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

	id := extractFeatureID(feature.ID)

	if hit := processPBFEntity("pmtiles", id, tags, coords,
		p.queryLower, p.params, p.conf, p.ont, p.geosCtx); hit != nil {
		collectHit(p.res, hit)
		if p.collectLimit > 0 && len(p.res.Hits) >= p.collectLimit {
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
		"housenumber",
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

func matchesGeometryCoarseFilter(
	g orb.Geometry,
	coords [][]float64,
	f *spatialFilter,
	params search.SearchParams,
	radiusMeters int,
) bool {
	switch g.(type) {
	case orb.Point:
		return len(coords) > 0 && matchesCoordSpatialFilter(coords[0], f, params, radiusMeters)
	case orb.MultiPoint:
		for _, coord := range coords {
			if matchesCoordSpatialFilter(coord, f, params, radiusMeters) {
				return true
			}
		}
		return false
	default:
		return geometryBoundMatchesFilter(g.Bound(), f)
	}
}

func matchesCoordSpatialFilter(coord []float64, f *spatialFilter, params search.SearchParams, radiusMeters int) bool {
	if len(coord) != 2 {
		return false
	}

	return matchesSpatialFilter(
		nanodegree(coord[1]),
		nanodegree(coord[0]),
		f.hasRadius,
		f.hasBbox,
		f.minLat,
		f.maxLat,
		f.minLon,
		f.maxLon,
		params,
		radiusMeters,
	)
}

func geometryBoundMatchesFilter(bound orb.Bound, f *spatialFilter) bool {
	if !f.hasRadius && !f.hasBbox {
		return true
	}

	filterBound := orb.Bound{
		Min: orb.Point{
			float64(f.minLon) / 1_000_000_000,
			float64(f.minLat) / 1_000_000_000,
		},
		Max: orb.Point{
			float64(f.maxLon) / 1_000_000_000,
			float64(f.maxLat) / 1_000_000_000,
		},
	}
	return filterBound.Intersects(bound)
}

func matchesPreciseFilter(g orb.Geometry, f *spatialFilter, params search.SearchParams, radiusMeters int) bool {
	if f.hasRadius {
		// Precise Distance check
		center := orb.Point{*params.Lon, *params.Lat}
		distMeters := localMetricDistanceFrom(g, center)
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

func localMetricDistanceFrom(g orb.Geometry, center orb.Point) float64 {
	projected := projectGeometryToLocalMeters(g, center)
	return planar.DistanceFrom(projected, orb.Point{0, 0})
}

func projectGeometryToLocalMeters(g orb.Geometry, center orb.Point) orb.Geometry {
	const earthRadiusMeters = 6_371_000.0
	metersPerLatDegree := earthRadiusMeters * math.Pi / 180
	metersPerLonDegree := metersPerLatDegree * math.Cos(center[1]*math.Pi/180)

	projectPoint := func(p orb.Point) orb.Point {
		return orb.Point{
			(p[0] - center[0]) * metersPerLonDegree,
			(p[1] - center[1]) * metersPerLatDegree,
		}
	}

	projectPoints := func(points []orb.Point) []orb.Point {
		projected := make([]orb.Point, 0, len(points))
		for _, point := range points {
			projected = append(projected, projectPoint(point))
		}
		return projected
	}

	switch geom := g.(type) {
	case orb.Point:
		return projectPoint(geom)
	case orb.MultiPoint:
		return orb.MultiPoint(projectPoints(geom))
	case orb.LineString:
		return orb.LineString(projectPoints(geom))
	case orb.MultiLineString:
		projected := make(orb.MultiLineString, 0, len(geom))
		for _, line := range geom {
			projected = append(projected, orb.LineString(projectPoints(line)))
		}
		return projected
	case orb.Polygon:
		projected := make(orb.Polygon, 0, len(geom))
		for _, ring := range geom {
			projected = append(projected, orb.Ring(projectPoints(ring)))
		}
		return projected
	case orb.MultiPolygon:
		projected := make(orb.MultiPolygon, 0, len(geom))
		for _, polygon := range geom {
			projectedPolygon := make(orb.Polygon, 0, len(polygon))
			for _, ring := range polygon {
				projectedPolygon = append(projectedPolygon, orb.Ring(projectPoints(ring)))
			}
			projected = append(projected, projectedPolygon)
		}
		return projected
	default:
		return g
	}
}

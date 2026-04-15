package osm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
	osmapi "github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
	"github.com/twpayne/go-geos"
	"golang.org/x/sync/errgroup"
)

// nodeCache provides a memory-efficient lookup for node coordinates.
type nodeCache interface {
	Set(id int64, lon, lat float64)
	Get(id int64) ([]float64, bool)
	Close()
}

// filteredNodeCache uses an in-memory map of packed uint64s to store coordinates.
// It only stores coordinates for nodes that were identified as "required" in a pre-pass.
type filteredNodeCache struct {
	required map[int64]struct{}
	nodes    map[int64]uint64
	mu       sync.RWMutex
}

func newFilteredNodeCache(required map[int64]struct{}) *filteredNodeCache {
	return &filteredNodeCache{
		required: required,
		nodes:    make(map[int64]uint64),
	}
}

func (m *filteredNodeCache) Set(id int64, lon, lat float64) {
	if m.required != nil {
		if _, ok := m.required[id]; !ok {
			return
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Pack two float32s into one uint64 to save memory
	m.nodes[id] = uint64(math.Float32bits(float32(lon)))<<32 | uint64(math.Float32bits(float32(lat)))
}

func (m *filteredNodeCache) Get(id int64) ([]float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.nodes[id]
	if !ok {
		return nil, false
	}
	lon := float64(math.Float32frombits(uint32(val >> 32)))
	lat := float64(math.Float32frombits(uint32(val)))
	return []float64{lon, lat}, true
}

func (m *filteredNodeCache) Close() {
	m.nodes = nil
	m.required = nil
}

// collectRequiredNodes scans the PBF once to find all node IDs used by ways and relations.
func collectRequiredNodes(inputPath string) (map[int64]struct{}, error) {
	slog.Info("Pass 1: collecting required node IDs from ways and relations...", "input", inputPath)
	start := time.Now()

	f, err := os.Open(inputPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Skip nodes for speed; we only need to look at ways and relations to see what nodes they reference.
	scanner := osmpbf.New(context.Background(), f, runtime.GOMAXPROCS(-1))
	scanner.SkipNodes = true
	defer scanner.Close()

	required := make(map[int64]struct{})
	count := 0
	for scanner.Scan() {
		obj := scanner.Object()
		switch o := obj.(type) {
		case *osmapi.Way:
			for _, n := range o.Nodes {
				if n.Lon == 0 && n.Lat == 0 { // Only if not already localized
					required[int64(n.ID)] = struct{}{}
				}
			}
		case *osmapi.Relation:
			for _, m := range o.Members {
				if m.Type == osmapi.TypeNode {
					required[m.Ref] = struct{}{}
				}
			}
		}
		count++
		if count%500000 == 0 {
			slog.Debug("Pass 1 progress", "objects", count, "required_nodes", len(required))
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("pass 1 scan error: %w", err)
	}

	slog.Info("Pass 1 finished", "required_nodes", len(required), "duration", time.Since(start))
	return required, nil
}

// GeometryMode represents the geometry mode for index building
type GeometryMode string

const (
	ModeGeopoint         GeometryMode = "geopoint"
	ModeGeopointCentroid GeometryMode = "geopoint-centroid"
	ModeGeoshapeBBox     GeometryMode = "geoshape-bbox"
	ModeGeoshapeSimplify GeometryMode = "geoshape-simplified"
	ModeGeoshapeFull     GeometryMode = "geoshape-full"
	ModeNoGeo            GeometryMode = "no-geo"
)

func roundToMeterAccuracy(f float64) float64 {
	shift := math.Pow(10, 5)
	return math.Floor(f*shift+0.5) / shift
}

func representativePoint(g *geos.Geom) (*geos.Geom, bool, error) {
	switch g.TypeID() {
	case geos.TypeIDPoint:
		return g, true, nil
	case geos.TypeIDLineString, geos.TypeIDLinearRing:
		return g.InterpolateNormalized(0.5), false, nil
	case geos.TypeIDMultiLineString:
		pt, err := midpointOfLongestLine(g)
		return pt, false, err
	case geos.TypeIDPolygon, geos.TypeIDMultiPolygon:
		centroid := g.Centroid()
		if g.Intersects(centroid) {
			return centroid, false, nil
		}
		return g.PointOnSurface(), false, nil
	case geos.TypeIDMultiPoint, geos.TypeIDGeometryCollection:
		return g.PointOnSurface(), false, nil
	default:
		return g.PointOnSurface(), false, nil
	}
}

func midpointOfLongestLine(multi *geos.Geom) (*geos.Geom, error) {
	n := multi.NumGeometries()
	if n == 0 {
		return nil, errors.New("empty geometry collection")
	}

	var longest *geos.Geom
	var longestLen float64

	for i := range n {
		component := multi.Geometry(i)
		l := component.Length()
		if longest == nil || l > longestLen {
			longest = component
			longestLen = l
		}
	}

	if longest == nil {
		return nil, errors.New("no valid components in multiline")
	}

	return longest.InterpolateNormalized(0.5), nil
}

func geomToGeoJSON(g *geos.Geom) map[string]any {
	switch g.TypeID() {
	case geos.TypeIDPoint:
		return map[string]any{
			"type":        "point",
			"coordinates": []float64{roundToMeterAccuracy(g.X()), roundToMeterAccuracy(g.Y())},
		}
	case geos.TypeIDLineString, geos.TypeIDLinearRing:
		coords := g.CoordSeq().ToCoords()
		for i := range coords {
			coords[i][0] = roundToMeterAccuracy(coords[i][0])
			coords[i][1] = roundToMeterAccuracy(coords[i][1])
		}
		return map[string]any{
			"type":        "linestring",
			"coordinates": coords,
		}
	case geos.TypeIDPolygon:
		ring := g.ExteriorRing()
		coords := ring.CoordSeq().ToCoords()
		for i := range coords {
			coords[i][0] = roundToMeterAccuracy(coords[i][0])
			coords[i][1] = roundToMeterAccuracy(coords[i][1])
		}
		return map[string]any{
			"type":        "polygon",
			"coordinates": [][][]float64{coords},
		}
	case geos.TypeIDMultiPoint:
		n := g.NumGeometries()
		coords := make([][]float64, 0, n)
		for i := range n {
			comp := g.Geometry(i)
			coords = append(coords, []float64{roundToMeterAccuracy(comp.X()), roundToMeterAccuracy(comp.Y())})
		}
		return map[string]any{
			"type":        "multipoint",
			"coordinates": coords,
		}
	case geos.TypeIDMultiLineString:
		n := g.NumGeometries()
		coords := make([][][]float64, 0, n)
		for i := range n {
			comp := g.Geometry(i)
			lineCoords := comp.CoordSeq().ToCoords()
			for j := range lineCoords {
				lineCoords[j][0] = roundToMeterAccuracy(lineCoords[j][0])
				lineCoords[j][1] = roundToMeterAccuracy(lineCoords[j][1])
			}
			coords = append(coords, lineCoords)
		}
		return map[string]any{
			"type":        "multilinestring",
			"coordinates": coords,
		}
	case geos.TypeIDMultiPolygon:
		n := g.NumGeometries()
		coords := make([][][][]float64, 0, n)
		for i := range n {
			comp := g.Geometry(i)
			ring := comp.ExteriorRing()
			ringCoords := ring.CoordSeq().ToCoords()
			for j := range ringCoords {
				ringCoords[j][0] = roundToMeterAccuracy(ringCoords[j][0])
				ringCoords[j][1] = roundToMeterAccuracy(ringCoords[j][1])
			}
			coords = append(coords, [][][]float64{ringCoords})
		}
		return map[string]any{
			"type":        "multipolygon",
			"coordinates": coords,
		}
	case geos.TypeIDGeometryCollection:
		n := g.NumGeometries()
		geometries := make([]map[string]any, 0, n)
		for i := range n {
			comp := g.Geometry(i)
			geometries = append(geometries, geomToGeoJSON(comp))
		}
		return map[string]any{
			"type":       "geometrycollection",
			"geometries": geometries,
		}
	default:
		centroid := g.Centroid()
		return map[string]any{
			"type":        "point",
			"coordinates": []float64{roundToMeterAccuracy(centroid.X()), roundToMeterAccuracy(centroid.Y())},
		}
	}
}

// BuildIndex builds a Bleve index from an OSM PBF file using paulmach/osm streaming scanner.
//
//nolint:revive,funlen
func BuildIndex(inputPath string, conf *config.Config, index bleve.Index) error {
	// Determine worker count
	workers := conf.BuildWorkers
	if workers <= 0 {
		workers = config.DefaultBuildWorkers
	}

	// Pass 1: Collect required node IDs
	requiredNodes, err := collectRequiredNodes(inputPath)
	if err != nil {
		slog.Warn("Pass 1 failed, will cache all nodes (high memory usage)", "error", err)
	}

	// Use parallel version if workers > 1
	if workers > 1 {
		return buildIndexParallel(inputPath, conf, index, workers, requiredNodes)
	}

	// Single-threaded version (original)
	slog.Info("Pass 2: building index", "path", conf.IndexPath, "input", inputPath)

	wdLookup, ont := setupBuildIndex(conf)

	// Build node lookup for ways/relations (needed for geometry reconstruction)
	nodeCoords := newFilteredNodeCache(requiredNodes)
	defer nodeCoords.Close()

	geosCtx := geos.NewContext()
	count := 0
	skipped := 0
	geoMode := GeometryMode(conf.GeometryMode)
	batch := index.NewBatch()
	batchSize := 500

	ectx := &entityContext{
		conf:      conf,
		index:     index,
		wdLookup:  wdLookup,
		ont:       ont,
		geosCtx:   geosCtx,
		batch:     batch,
		geoMode:   geoMode,
		batchSize: batchSize,
	}

	// Open PBF
	file, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := osmpbf.New(context.Background(), file, runtime.GOMAXPROCS(-1))
	if conf.NodesOnly {
		scanner.SkipWays = true
		scanner.SkipRelations = true
	}
	defer scanner.Close()

	totalScanned := 0
	logInterval := 50000

	for scanner.Scan() {
		obj := scanner.Object()
		totalScanned++

		var found bool

		switch o := obj.(type) {
		case *osmapi.Node:
			// Store node coordinates for way lookup
			nodeCoords.Set(int64(o.ID), o.Lon, o.Lat)

			tags := o.TagMap()
			coords := [][]float64{{o.Lon, o.Lat}}
			found = processEntity(ectx, "node", int64(o.ID), tags, coords)

		case *osmapi.Way:
			tags := o.TagMap()
			coords := make([][]float64, 0, len(o.Nodes))
			for _, node := range o.Nodes {
				if node.Lon != 0 || node.Lat != 0 {
					// Way has embedded coordinates (add-locations-to-ways)
					coords = append(coords, []float64{node.Lon, node.Lat})
				} else if nc, ok := nodeCoords.Get(int64(node.ID)); ok {
					coords = append(coords, nc)
				}
			}
			if len(coords) < 2 {
				skipped++
				continue
			}
			found = processEntity(ectx, "way", int64(o.ID), tags, coords)

		case *osmapi.Relation:
			tags := o.TagMap()
			// Get first member node coordinate as representative point
			var coords [][]float64
			for _, member := range o.Members {
				if member.Type == osmapi.TypeNode {
					if nc, ok := nodeCoords.Get(member.Ref); ok {
						coords = [][]float64{nc}
						break
					}
				} else if member.Type == osmapi.TypeWay {
					// Member might have embedded Lat/Lon
					if member.Lat != 0 || member.Lon != 0 {
						coords = [][]float64{{member.Lon, member.Lat}}
						break
					}
				}
			}
			if len(coords) == 0 {
				skipped++
				continue
			}
			found = processEntity(ectx, "relation", int64(o.ID), tags, coords)
		}

		if found {
			count++
			if count%batchSize == 0 {
				if err := index.Batch(ectx.batch); err != nil {
					return err
				}
				ectx.batch = index.NewBatch()
			}
		} else {
			skipped++
		}

		if totalScanned%logInterval == 0 {
			slog.Info(
				"indexing progress",
				"scanned",
				totalScanned,
				"indexed",
				count,
				"skipped",
				skipped,
			)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("PBF scan error: %w", err)
	}

	// Flush remaining batch
	if ectx.batch.Size() > 0 {
		if err := index.Batch(ectx.batch); err != nil {
			return err
		}
	}

	slog.Info("Finished!", "indexed", count, "skipped", skipped)
	return nil
}

func setupBuildIndex(conf *config.Config) (*WikidataLookup, *PlaceTypeOntology) {
	// Load Wikidata importance lookup if configured
	var wdLookup *WikidataLookup
	if conf.WikidataImportance != "" {
		var err error
		wdLookup, err = LoadWikidataImportance(conf.WikidataImportance)
		if err != nil {
			slog.Warn("failed to load wikidata importance file, continuing without it", "error", err)
		} else {
			slog.Info("loaded wikidata importance scores", "count", wdLookup.Size())
		}
	}

	// Load place type ontology if configured
	var ont *PlaceTypeOntology
	if conf.OntologyPath != "" {
		var err error
		ont, err = LoadOntologyFromCSV(conf.OntologyPath)
		if err != nil {
			slog.Warn("failed to load ontology file, continuing without it", "error", err)
		} else {
			slog.Info("loaded place type ontology", "entries", len(ont.levels))
		}
	} else {
		ont = DefaultOntology()
	}
	return wdLookup, ont
}

type entityContext struct {
	conf      *config.Config
	index     bleve.Index
	wdLookup  *WikidataLookup
	ont       *PlaceTypeOntology
	geosCtx   *geos.Context
	batch     *bleve.Batch
	geoMode   GeometryMode
	batchSize int
}

func processEntity(
	ctx *entityContext,
	entityType string,
	id int64,
	tags map[string]string,
	coords [][]float64,
) bool {
	if len(tags) == 0 {
		return false
	}

	classifications := ClassifyMulti(tags, &ctx.conf.Importance, ctx.ont)
	if len(classifications) == 0 {
		return false
	}

	// Check --only-named filter
	if ctx.conf.OnlyNamed && !hasAnyName(tags, ctx.conf.Languages) {
		return false
	}

	// Find best classification by importance with tie-breaking
	best := selectBestClassification(classifications, tags)

	// Apply Wikidata importance boost
	if ctx.wdLookup != nil && tags["wikidata"] != "" {
		applyWikidataBoost(ctx, tags, best)
	}

	// Build geometry
	var geom any
	if ctx.geoMode != ModeNoGeo {
		var err error
		geom, err = createGeometryFromCoords(coords, ctx.geoMode, ctx.conf.SimplificationTol, ctx.geosCtx)
		if err != nil {
			return false
		}
	}

	fParams := featureParams{
		entityType:      entityType,
		id:              id,
		tags:            tags,
		best:            best,
		geom:            geom,
		conf:            ctx.conf,
		classifications: classifications,
	}
	feature := buildFeatureFromTags(fParams)

	if err := ctx.batch.Index(feature.ID, search.FeatureToMap(feature)); err != nil {
		return false
	}

	return true
}

func EnhanceName(tags map[string]string) {
	name := tags["name"]
	if name == "" {
		return
	}

	for _, k := range []string{"brand", "operator", "religion", "denomination"} {
		v := tags[k]
		if v != "" && v != "yes" && v != "no" {
			// If name doesn't already contain the brand/operator/etc (case-insensitive)
			if !strings.Contains(strings.ToLower(name), strings.ToLower(v)) {
				tags["name"] = fmt.Sprintf("%s (%s)", name, v)
				return // Only add one enhancement
			}
		}
	}
}

func NormalizeNameTag(tags map[string]string, languages []string) {
	if tags["name"] != "" {
		return
	}
	for _, lang := range languages {
		if val, ok := tags["name:"+lang]; ok && val != "" {
			tags["name"] = val
			break
		}
	}
	// Final fallback to English if no match in preferred languages
	if tags["name"] == "" && tags["name:en"] != "" {
		tags["name"] = tags["name:en"]
	}
}

func hasAnyName(tags map[string]string, languages []string) bool {
	if tags["name"] != "" {
		return true
	}
	for _, alt := range []string{"alt_name", "old_name", "short_name"} {
		if tags[alt] != "" {
			return true
		}
	}
	for _, lang := range languages {
		if tags["name:"+lang] != "" {
			return true
		}
	}
	return false
}

func selectBestClassification(classifications []*Classification, tags map[string]string) *Classification {
	best := classifications[0]
	for _, c := range classifications[1:] {
		if c.Importance > best.Importance {
			best = c
		} else if c.Importance == best.Importance {
			if c.OntLevel > best.OntLevel {
				best = c
			} else if c.OntLevel == best.OntLevel {
				cPop := parsePopulation(tags)
				bestPop := parsePopulationForClassification(best.Class, best.Subtype, tags)
				if cPop > bestPop {
					best = c
				}
			}
		}
	}
	return best
}

func applyWikidataBoost(ctx *entityContext, tags map[string]string, best *Classification) {
	primaryLang := ""
	if len(ctx.conf.Languages) > 0 {
		primaryLang = ctx.conf.Languages[0]
	}
	for _, lang := range ctx.conf.Languages {
		if _, ok := tags["name:"+lang]; ok {
			primaryLang = lang
			break
		}
	}
	var wdImportance float64
	if primaryLang != "" {
		wdImportance = ctx.wdLookup.GetImportanceForLang(tags["wikidata"], primaryLang)
	} else {
		wdImportance = ctx.wdLookup.GetImportance(tags["wikidata"])
	}
	if wdImportance > 0 {
		best.Importance += wdImportance * 10.0
	}
}

type featureParams struct {
	entityType      string
	id              int64
	tags            map[string]string
	best            *Classification
	geom            any
	conf            *config.Config
	classifications []*Classification
}

func buildFeatureFromTags(p featureParams) *search.Feature {
	NormalizeNameTag(p.tags, p.conf.Languages)
	EnhanceName(p.tags) // Add brand/operator/etc in parentheses

	feature := &search.Feature{
		ID:           fmt.Sprintf("%s/%d", p.entityType, p.id),
		Name:         p.tags["name"],
		Names:        make(map[string]string),
		Importance:   p.best.Importance,
		Geometry:     p.geom,
		Class:        p.best.Class,
		Subtype:      p.best.Subtype,
		Phone:        p.tags["phone"],
		Wheelchair:   p.tags["wheelchair"],
		OpeningHours: p.tags["opening_hours"],
	}

	if p.tags["phone"] == "" && p.tags["contact:phone"] != "" {
		feature.Phone = p.tags["contact:phone"]
	}

	if p.conf.DisableImportance {
		feature.Importance = 0
	}

	if !p.conf.DisableClassSubtype {
		classes := make([]string, len(p.classifications))
		subtypes := make([]string, len(p.classifications))
		for i, c := range p.classifications {
			classes[i] = c.Class
			subtypes[i] = c.Subtype
		}
		feature.Classes = classes
		feature.Subtypes = subtypes
	}

	addNamesToFeature(feature, p.tags, p.conf)
	addAddressToFeature(feature, p.tags, p.conf)
	return feature
}

func addNamesToFeature(feature *search.Feature, tags map[string]string, conf *config.Config) {
	if !conf.DisableAltNames {
		for _, alt := range []string{"alt_name", "old_name", "short_name", "brand", "operator"} {
			if val, ok := tags[alt]; ok {
				feature.Names[alt] = val
			}
		}
		for _, lang := range conf.Languages {
			if name, ok := tags["name:"+lang]; ok {
				feature.Names["name:"+lang] = name
			}
			for _, alt := range []string{"alt_name", "old_name", "short_name"} {
				if val, ok := tags[alt+":"+lang]; ok {
					feature.Names[alt+":"+lang] = val
				}
			}
		}
	} else {
		for _, lang := range conf.Languages {
			if name, ok := tags["name:"+lang]; ok {
				feature.Names["name:"+lang] = name
			}
		}
	}
}

func addAddressToFeature(feature *search.Feature, tags map[string]string, conf *config.Config) {
	if conf.StoreAddress {
		feature.Address = make(map[string]string)
		addrKeys := []string{
			"addr:housenumber", "addr:street", "addr:city", "addr:postcode",
			"addr:country", "addr:state", "addr:district", "addr:suburb",
			"addr:neighbourhood", "addr:floor", "addr:unit", "level",
		}
		for _, k := range addrKeys {
			if val, ok := tags[k]; ok {
				feature.Address[k] = val
			}
		}
	}
}

// CreateGeometry creates a GeoJSON-compatible geometry from an OSM object.
func CreateGeometry(obj osmapi.Object, mode GeometryMode, tolerance float64, ctx *geos.Context) (any, error) {
	var g *geos.Geom

	switch o := obj.(type) {
	case *osmapi.Node:
		g = ctx.NewPointFromXY(o.Lon, o.Lat)
	case *osmapi.Way:
		coords := make([][]float64, len(o.Nodes))
		for i, n := range o.Nodes {
			coords[i] = []float64{n.Lon, n.Lat}
		}
		if len(coords) < 2 {
			return nil, errors.New("way with fewer than 2 nodes")
		}
		if len(coords) >= 4 && coords[0][0] == coords[len(coords)-1][0] && coords[0][1] == coords[len(coords)-1][1] {
			g = ctx.NewPolygon([][][]float64{coords})
		} else {
			g = ctx.NewLineString(coords)
		}
	case *osmapi.Relation:
		if o.Bounds == nil {
			return nil, errors.New("relation without bounds")
		}
		g = ctx.NewGeomFromBounds(o.Bounds.MinLon, o.Bounds.MinLat, o.Bounds.MaxLon, o.Bounds.MaxLat)
	default:
		return nil, errors.New("unsupported object type")
	}

	if g == nil {
		return nil, errors.New("could not create geometry")
	}
	return processGeomByMode(g, mode, tolerance)
}

func processGeomByMode(g *geos.Geom, mode GeometryMode, tolerance float64) (any, error) {
	switch mode {
	case ModeGeopoint, ModeGeopointCentroid:
		var pt *geos.Geom
		var err error
		if mode == ModeGeopointCentroid {
			pt = g.Centroid()
		} else {
			pt, _, err = representativePoint(g)
		}
		if err != nil || pt == nil {
			return nil, fmt.Errorf("could not create geopoint: %w", err)
		}
		return map[string]any{
			"lat": roundToMeterAccuracy(pt.Y()),
			"lon": roundToMeterAccuracy(pt.X()),
		}, nil
	case ModeGeoshapeBBox:
		return geomToGeoJSON(g.Envelope()), nil
	case ModeGeoshapeSimplify:
		return geomToGeoJSON(g.TopologyPreserveSimplify(tolerance)), nil
	case ModeGeoshapeFull:
		return geomToGeoJSON(g), nil
	case ModeNoGeo:
		pt, _, _ := representativePoint(g)
		return map[string]any{
			"lat": roundToMeterAccuracy(pt.Y()),
			"lon": roundToMeterAccuracy(pt.X()),
		}, nil
	default:
		pt, _, _ := representativePoint(g)
		return map[string]any{
			"lat": roundToMeterAccuracy(pt.Y()),
			"lon": roundToMeterAccuracy(pt.X()),
		}, nil
	}
}

// createGeometryFromCoords creates a geometry from a list of coordinates
func createGeometryFromCoords(
	coords [][]float64,
	mode GeometryMode,
	tolerance float64,
	ctx *geos.Context,
) (any, error) {
	if len(coords) == 0 {
		return nil, errors.New("no coordinates")
	}

	var g *geos.Geom
	if len(coords) == 1 {
		g = ctx.NewPointFromXY(coords[0][0], coords[0][1])
	} else {
		if len(coords) >= 4 && coords[0][0] == coords[len(coords)-1][0] && coords[0][1] == coords[len(coords)-1][1] {
			g = ctx.NewPolygon([][][]float64{coords})
		} else {
			g = ctx.NewLineString(coords)
		}
	}

	if g == nil {
		return nil, errors.New("could not create geometry")
	}
	return processGeomByMode(g, mode, tolerance)
}

// workItem represents an OSM entity to be processed by a worker.
type workItem struct {
	ID     string
	Type   string
	Tags   map[string]string
	Coords [][]float64
}

// processedItem represents the result of processing a work item.
type processedItem struct {
	ID      string
	Feature map[string]any
}

// buildIndexParallel builds an index using multiple workers for entity processing.
func buildIndexParallel(
	inputPath string,
	conf *config.Config,
	index bleve.Index,
	workers int,
	requiredNodes map[int64]struct{},
) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slog.InfoContext(
		ctx,
		"Pass 2: building index with parallel workers",
		"path",
		conf.IndexPath,
		"input",
		inputPath,
		"workers",
		workers,
	)

	wdLookup, ont := setupBuildIndex(conf)

	nodeCoords := newFilteredNodeCache(requiredNodes)
	defer nodeCoords.Close()
	workChan := make(chan *workItem, workers*100)
	resultChan := make(chan *processedItem, workers*100)

	g, egCtx := errgroup.WithContext(ctx)
	for range workers {
		g.Go(func() error {
			workerGeosCtx := geos.NewContext()
			params := workerParams{
				workChan:   workChan,
				resultChan: resultChan,
				conf:       conf,
				wdLookup:   wdLookup,
				ont:        ont,
				geosCtx:    workerGeosCtx,
			}
			return processWorker(egCtx, params)
		})
	}

	var collectorErr error
	var collectorWg sync.WaitGroup
	collectorWg.Go(func() {
		collectorErr = runCollector(ctx, index, resultChan, cancel)
	})

	totalScanned, scannerSkipped, producerErr := runProducer(egCtx, inputPath, conf, nodeCoords, workChan)
	close(workChan)

	// Wait for workers to finish
	workersErr := g.Wait()

	// Now that workers are done, we can close resultChan to signal collector to finish
	close(resultChan)
	collectorWg.Wait()

	// Prioritize errors:
	if collectorErr != nil {
		return collectorErr
	}
	if workersErr != nil {
		return workersErr
	}
	if producerErr != nil {
		return producerErr
	}

	slog.Info("Finished!", "scanned", totalScanned, "skipped", scannerSkipped)
	return nil
}

//nolint:revive
func runCollector(
	ctx context.Context,
	index bleve.Index,
	resultChan <-chan *processedItem,
	cancel context.CancelFunc,
) error {
	batch := index.NewBatch()
	batchSize := 500
	count := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case item, ok := <-resultChan:
			if !ok {
				if batch.Size() > 0 {
					if err := index.Batch(batch); err != nil {
						return fmt.Errorf("collector final batch write error: %w", err)
					}
				}
				return nil
			}
			if item.Feature != nil {
				if err := batch.Index(item.ID, item.Feature); err != nil {
					cancel()
					return fmt.Errorf("collector batch index error: %w", err)
				}
				count++
				if count%batchSize == 0 {
					if err := index.Batch(batch); err != nil {
						cancel()
						return fmt.Errorf("collector batch write error: %w", err)
					}
					batch = index.NewBatch()
				}
			}
		}
	}
}

func runProducer(
	ctx context.Context,
	inputPath string,
	conf *config.Config,
	nodeCoords nodeCache,
	workChan chan<- *workItem,
) (scanned, skipped int, err error) {
	file, err := os.Open(inputPath)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := osmpbf.New(ctx, file, runtime.GOMAXPROCS(-1))
	if conf.NodesOnly {
		scanner.SkipWays = true
		scanner.SkipRelations = true
	}
	defer scanner.Close()

	totalScanned := 0
	logInterval := 50000

	for scanner.Scan() {
		obj := scanner.Object()
		totalScanned++
		item := buildWorkItem(obj, nodeCoords)
		if item != nil {
			select {
			case <-ctx.Done():
				return totalScanned, skipped, ctx.Err()
			case workChan <- item:
			}
		} else {
			skipped++
		}

		if totalScanned%logInterval == 0 {
			slog.InfoContext(
				ctx,
				"indexing progress",
				"scanned",
				totalScanned,
				"skipped",
				skipped,
			)
		}
	}
	return totalScanned, skipped, scanner.Err()
}

func buildWorkItem(obj osmapi.Object, nodeCoords nodeCache) *workItem {
	switch o := obj.(type) {
	case *osmapi.Node:
		nodeCoords.Set(int64(o.ID), o.Lon, o.Lat)
		tags := o.TagMap()
		if len(tags) == 0 {
			return nil
		}
		return &workItem{
			ID:     fmt.Sprintf("node/%d", int64(o.ID)),
			Type:   "node",
			Tags:   tags,
			Coords: [][]float64{{o.Lon, o.Lat}},
		}
	case *osmapi.Way:
		tags := o.TagMap()
		if len(tags) == 0 {
			return nil
		}
		coords := extractWayCoords(o, nodeCoords)
		if len(coords) < 2 {
			return nil
		}
		return &workItem{ID: fmt.Sprintf("way/%d", int64(o.ID)), Type: "way", Tags: tags, Coords: coords}
	case *osmapi.Relation:
		tags := o.TagMap()
		if len(tags) == 0 {
			return nil
		}
		coords := extractRelationCoords(o, nodeCoords)
		if len(coords) == 0 {
			return nil
		}
		return &workItem{ID: fmt.Sprintf("relation/%d", int64(o.ID)), Type: "relation", Tags: tags, Coords: coords}
	default:
		return nil
	}
}

func extractWayCoords(o *osmapi.Way, nodeCoords nodeCache) [][]float64 {
	coords := make([][]float64, 0, len(o.Nodes))
	for _, node := range o.Nodes {
		if node.Lon != 0 || node.Lat != 0 {
			coords = append(coords, []float64{node.Lon, node.Lat})
		} else if nc, ok := nodeCoords.Get(int64(node.ID)); ok {
			coords = append(coords, nc)
		}
	}
	return coords
}

func extractRelationCoords(o *osmapi.Relation, nodeCoords nodeCache) [][]float64 {
	for _, member := range o.Members {
		switch member.Type {
		case osmapi.TypeNode:
			if nc, ok := nodeCoords.Get(member.Ref); ok {
				return [][]float64{nc}
			}
		case osmapi.TypeWay:
			if member.Lat != 0 || member.Lon != 0 {
				return [][]float64{{member.Lon, member.Lat}}
			}
		case osmapi.TypeRelation, osmapi.TypeChangeset, osmapi.TypeNote, osmapi.TypeUser, osmapi.TypeBounds:
			// Not used for coords
		}
	}
	return nil
}

type workerParams struct {
	workChan   <-chan *workItem
	resultChan chan<- *processedItem
	conf       *config.Config
	wdLookup   *WikidataLookup
	ont        *PlaceTypeOntology
	geosCtx    *geos.Context
}

func processWorker(ctx context.Context, p workerParams) error {
	for {
		select {
		case <-ctx.Done():
			go func() {
				for range p.workChan {
					continue
				}
			}()
			return ctx.Err()
		case item, ok := <-p.workChan:
			if !ok {
				return nil
			}

			classifications := ClassifyMulti(item.Tags, &p.conf.Importance, p.ont)
			if len(classifications) == 0 {
				p.resultChan <- &processedItem{ID: item.ID}
				continue
			}

			if p.conf.OnlyNamed && !hasAnyName(item.Tags, p.conf.Languages) {
				p.resultChan <- &processedItem{ID: item.ID}
				continue
			}

			best := selectBestClassification(classifications, item.Tags)
			if p.wdLookup != nil && item.Tags["wikidata"] != "" {
				applyWikidataBoostWorker(p.conf, item.Tags, p.wdLookup, best)
			}

			var geom any
			if GeometryMode(p.conf.GeometryMode) != ModeNoGeo {
				var err error
				geom, err = createGeometryFromCoords(
					item.Coords,
					GeometryMode(p.conf.GeometryMode),
					p.conf.SimplificationTol,
					p.geosCtx,
				)
				if err != nil {
					p.resultChan <- &processedItem{ID: item.ID}
					continue
				}
			}

			fParams := featureParams{
				entityType:      item.Type,
				id:              0,
				tags:            item.Tags,
				best:            best,
				geom:            geom,
				conf:            p.conf,
				classifications: classifications,
			}
			feature := buildFeatureFromTags(fParams)
			feature.ID = item.ID // Use ID from work item

			p.resultChan <- &processedItem{
				ID:      item.ID,
				Feature: search.FeatureToMap(feature),
			}
		}
	}
}

func applyWikidataBoostWorker(
	conf *config.Config,
	tags map[string]string,
	wdLookup *WikidataLookup,
	best *Classification,
) {
	primaryLang := ""
	if len(conf.Languages) > 0 {
		primaryLang = conf.Languages[0]
	}
	for _, lang := range conf.Languages {
		if _, ok := tags["name:"+lang]; ok {
			primaryLang = lang
			break
		}
	}
	var wdImportance float64
	if primaryLang != "" {
		wdImportance = wdLookup.GetImportanceForLang(tags["wikidata"], primaryLang)
	} else {
		wdImportance = wdLookup.GetImportance(tags["wikidata"])
	}
	if wdImportance > 0 {
		best.Importance += wdImportance * 10.0
	}
}

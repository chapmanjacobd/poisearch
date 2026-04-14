package osm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"runtime"
	"sync"

	"github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
	osmapi "github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
	"github.com/twpayne/go-geos"
	"golang.org/x/sync/errgroup"
)

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
// Deduplication is handled by Bleve's upsert semantics: each OSM entity has a globally unique
// ID (type/id), so re-indexing the same document ID overwrites the previous version.
//
//nolint:funlen,cyclop // Index building requires coordinating streaming, classification, and geometry
func BuildIndex(inputPath string, conf *config.Config, index bleve.Index) error {
	// Determine worker count
	workers := conf.BuildWorkers
	if workers <= 0 {
		workers = config.DefaultBuildWorkers
	}

	// Use parallel version if workers > 1
	if workers > 1 {
		return buildIndexParallel(inputPath, conf, index, workers)
	}

	// Single-threaded version (original)
	slog.Info("building index", "path", conf.IndexPath, "input", inputPath)

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

	// Build node lookup for ways/relations (needed for geometry reconstruction)
	nodeCoords := make(map[int64][]float64)

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

	// Open PBF with parallel decoders
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

		var err error
		var ok bool

		switch o := obj.(type) {
		case *osmapi.Node:
			// Store node coordinates for way lookup
			nodeCoords[int64(o.ID)] = []float64{o.Lon, o.Lat}

			tags := o.TagMap()
			coords := [][]float64{{o.Lon, o.Lat}}
			ok, err = processEntity(ectx, "node", int64(o.ID), tags, coords)

		case *osmapi.Way:
			tags := o.TagMap()
			coords := make([][]float64, 0, len(o.Nodes))
			for _, node := range o.Nodes {
				if node.Lon != 0 || node.Lat != 0 {
					// Way has embedded coordinates (add-locations-to-ways)
					coords = append(coords, []float64{node.Lon, node.Lat})
				} else if nc, ok := nodeCoords[int64(node.ID)]; ok {
					coords = append(coords, nc)
				}
			}
			if len(coords) < 2 {
				skipped++
				continue
			}
			ok, err = processEntity(ectx, "way", int64(o.ID), tags, coords)

		case *osmapi.Relation:
			tags := o.TagMap()
			// Get first member node coordinate as representative point
			var coords [][]float64
			for _, member := range o.Members {
				if member.Type == osmapi.TypeNode {
					if nc, ok := nodeCoords[member.Ref]; ok {
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
			ok, err = processEntity(ectx, "relation", int64(o.ID), tags, coords)
		}

		if err != nil {
			return err
		}
		if ok {
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
	if batch.Size() > 0 {
		if err := index.Batch(batch); err != nil {
			return err
		}
	}

	slog.Info("Finished!", "indexed", count, "skipped", skipped)
	return nil
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
) (bool, error) {
	if len(tags) == 0 {
		return false, nil
	}

	classifications := ClassifyMulti(tags, &ctx.conf.Importance, ctx.ont)
	if len(classifications) == 0 {
		return false, nil
	}

	// Check --only-named filter
	if ctx.conf.OnlyNamed {
		hasName := tags["name"] != ""
		if !hasName {
			for _, alt := range []string{"alt_name", "old_name", "short_name"} {
				if tags[alt] != "" {
					hasName = true
					break
				}
			}
			if !hasName {
				for _, lang := range ctx.conf.Languages {
					if tags["name:"+lang] != "" {
						hasName = true
						break
					}
				}
			}
		}
		if !hasName {
			return false, nil
		}
	}

	// Find best classification by importance with tie-breaking
	best := classifications[0]
	for _, c := range classifications[1:] {
		if c.Importance > best.Importance {
			best = c
		} else if c.Importance == best.Importance {
			// Tie-breaking: prefer finer granularity (higher ontological level)
			if c.OntLevel > best.OntLevel {
				best = c
			} else if c.OntLevel == best.OntLevel {
				// Tie-break by population (higher wins)
				cPop := parsePopulation(tags)
				bestPop := parsePopulationForClassification(best.Class, best.Subtype, tags)
				if cPop > bestPop {
					best = c
				}
			}
		}
	}

	// Apply Wikidata importance boost (language-aware)
	if ctx.wdLookup != nil && tags["wikidata"] != "" {
		// Detect primary language from tags (prefer name:XX tags, fallback to config)
		primaryLang := ""
		if len(ctx.conf.Languages) > 0 {
			primaryLang = ctx.conf.Languages[0]
		}
		// Check if entity has language-specific name tags
		for _, lang := range ctx.conf.Languages {
			if _, ok := tags["name:"+lang]; ok {
				primaryLang = lang
				break
			}
		}
		// Use language-aware lookup if we have a language, otherwise use highest score
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

	// Build geometry
	var geom any
	if ctx.geoMode != ModeNoGeo {
		var err error
		geom, err = createGeometryFromCoords(coords, ctx.geoMode, ctx.conf.SimplificationTol, ctx.geosCtx)
		if err != nil {
			slog.Debug("skipping entity (bad geometry)", "id", id, "error", err)
			return false, nil
		}
	}

	feature := &search.Feature{
		ID:         fmt.Sprintf("%s/%d", entityType, id),
		Name:       tags["name"],
		Names:      make(map[string]string),
		Importance: best.Importance,
		Geometry:   geom,
		Class:      best.Class,
		Subtype:    best.Subtype,
	}

	if ctx.conf.DisableImportance {
		feature.Importance = 0
	}

	if !ctx.conf.DisableClassSubtype {
		classes := make([]string, len(classifications))
		subtypes := make([]string, len(classifications))
		for i, c := range classifications {
			classes[i] = c.Class
			subtypes[i] = c.Subtype
		}
		feature.Classes = classes
		feature.Subtypes = subtypes
	}

	if !ctx.conf.DisableAltNames {
		for _, alt := range []string{"alt_name", "old_name", "short_name", "brand", "operator"} {
			if val, ok := tags[alt]; ok {
				feature.Names[alt] = val
			}
		}
		for _, lang := range ctx.conf.Languages {
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
		for _, lang := range ctx.conf.Languages {
			if name, ok := tags["name:"+lang]; ok {
				feature.Names["name:"+lang] = name
			}
		}
	}

	// Extract address fields when configured
	if ctx.conf.StoreAddress {
		feature.Address = make(map[string]string)
		addrKeys := []string{
			"addr:housenumber", "addr:street", "addr:city", "addr:postcode",
			"addr:country", "addr:state", "addr:district", "addr:suburb",
			"addr:neighbourhood",
		}
		for _, k := range addrKeys {
			if val, ok := tags[k]; ok {
				feature.Address[k] = val
			}
		}
	}

	// Attach Wikidata redirect titles as alternate names when configured
	if ctx.conf.IndexWikidataRedirects && ctx.wdLookup != nil && tags["wikidata"] != "" {
		redirects := ctx.wdLookup.GetRedirects(tags["wikidata"])
		if len(redirects) > 0 {
			feature.WikidataRedirects = redirects
		}
	}

	if err := ctx.batch.Index(feature.ID, search.FeatureToMap(feature)); err != nil {
		slog.Error("error indexing feature", "id", feature.ID, "error", err)
		return false, nil
	}

	return true, nil
}

// CreateGeometry creates a GeoJSON-compatible geometry from an OSM object.
// Used by the direct PBF search path.
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
		// Closed way → potential polygon
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

	if mode == ModeGeopoint || mode == ModeGeopointCentroid {
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
	}

	var finalGeom *geos.Geom
	switch mode {
	case ModeGeoshapeBBox:
		finalGeom = g.Envelope()
	case ModeGeoshapeSimplify:
		finalGeom = g.TopologyPreserveSimplify(tolerance)
	case ModeGeoshapeFull:
		finalGeom = g
	case ModeGeopoint, ModeGeopointCentroid, ModeNoGeo:
		pt, _, err := representativePoint(g)
		if err != nil || pt == nil {
			return nil, fmt.Errorf("could not create default geopoint: %w", err)
		}
		return map[string]any{
			"lat": roundToMeterAccuracy(pt.Y()),
			"lon": roundToMeterAccuracy(pt.X()),
		}, nil
	}

	return geomToGeoJSON(finalGeom), nil
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
		// Closed ring → polygon
		if len(coords) >= 4 && coords[0][0] == coords[len(coords)-1][0] && coords[0][1] == coords[len(coords)-1][1] {
			g = ctx.NewPolygon([][][]float64{coords})
		} else {
			g = ctx.NewLineString(coords)
		}
	}

	if g == nil {
		return nil, errors.New("could not create geometry")
	}

	if mode == ModeGeopoint || mode == ModeGeopointCentroid {
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
	}

	var finalGeom *geos.Geom
	switch mode {
	case ModeGeoshapeBBox:
		finalGeom = g.Envelope()
	case ModeGeoshapeSimplify:
		finalGeom = g.TopologyPreserveSimplify(tolerance)
	case ModeGeoshapeFull:
		finalGeom = g
	case ModeGeopoint, ModeGeopointCentroid, ModeNoGeo:
		pt, _, err := representativePoint(g)
		if err != nil || pt == nil {
			return nil, fmt.Errorf("could not create default geopoint: %w", err)
		}
		return map[string]any{
			"lat": roundToMeterAccuracy(pt.Y()),
			"lon": roundToMeterAccuracy(pt.X()),
		}, nil
	}

	return geomToGeoJSON(finalGeom), nil
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
//
// Architecture:
//   Producer (PBF scanner) → workChan → Workers (classify + geometry) → resultChan → Collector (batch writes to index)
//
// Each worker gets its own GEOS context for thread safety. The collector is the ONLY
// goroutine that writes to the Bleve index, avoiding concurrency issues.
//
//nolint:funlen,cyclop // Multi-threaded building requires coordinating channels, workers, and batching
func buildIndexParallel(inputPath string, conf *config.Config, index bleve.Index, workers int) error {
	slog.Info("building index with parallel workers", "path", conf.IndexPath, "input", inputPath, "workers", workers)

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

	// Shared node lookup for way/relation geometry reconstruction
	// Use sync.Map for concurrent access
	var nodeCoords sync.Map

	// Channels for work distribution
	// Buffer size = workers * 100 to provide adequate pipeline depth
	workChan := make(chan *workItem, workers*100)
	resultChan := make(chan *processedItem, workers*100)

	// Create a cancellable context for workers
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start worker goroutines
	g, ctx := errgroup.WithContext(ctx)

	for i := range workers {
		workerID := i
		g.Go(func() error {
			// Each worker gets its own GEOS context for thread safety
			workerGeosCtx := geos.NewContext()

			// Workers return feature data, they DO NOT write to the index directly
			// (Bleve is not thread-safe for concurrent writes)
			return processWorker(ctx, workerID, workChan, resultChan, conf, wdLookup, ont, workerGeosCtx)
		})
	}

	// Start collector goroutine (SINGLE writer to the index)
	var collectorErr error
	var collectorMu sync.Mutex
	var collectorWg sync.WaitGroup
	collectorWg.Add(1)

	go func() {
		defer collectorWg.Done()

		batch := index.NewBatch()
		batchSize := 500
		count := 0
		skipped := 0

		for item := range resultChan {
			if item.Feature != nil {
				// Index the feature in the collector's batch
				if err := batch.Index(item.ID, item.Feature); err != nil {
					collectorMu.Lock()
					collectorErr = fmt.Errorf("collector batch index error: %w", err)
					collectorMu.Unlock()
					cancel() // Cancel workers on error
					return
				}
				count++

				// Flush batch when it reaches the limit
				if count%batchSize == 0 {
					if err := index.Batch(batch); err != nil {
						collectorMu.Lock()
						collectorErr = fmt.Errorf("collector batch write error: %w", err)
						collectorMu.Unlock()
						cancel() // Cancel workers on error
						return
					}
					batch = index.NewBatch()
				}
			} else {
				skipped++
			}
		}

		// Flush remaining batch
		if batch.Size() > 0 {
			if err := index.Batch(batch); err != nil {
				collectorMu.Lock()
				collectorErr = fmt.Errorf("collector final batch write error: %w", err)
				collectorMu.Unlock()
			}
		}
	}()

	// Open PBF with parallel decoders
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

	// Producer: scan objects and distribute to workers
	totalScanned := 0
	scannerSkipped := 0
	logInterval := 50000

	for scanner.Scan() {
		obj := scanner.Object()
		totalScanned++

		var item *workItem

		switch o := obj.(type) {
		case *osmapi.Node:
			// Store node coordinates for way lookup
			nodeCoords.Store(int64(o.ID), []float64{o.Lon, o.Lat})

			tags := o.TagMap()
			if len(tags) == 0 {
				scannerSkipped++
				continue
			}
			item = &workItem{
				ID:     fmt.Sprintf("node/%d", int64(o.ID)),
				Type:   "node",
				Tags:   tags,
				Coords: [][]float64{{o.Lon, o.Lat}},
			}

		case *osmapi.Way:
			tags := o.TagMap()
			if len(tags) == 0 {
				scannerSkipped++
				continue
			}
			coords := make([][]float64, 0, len(o.Nodes))
			for _, node := range o.Nodes {
				if node.Lon != 0 || node.Lat != 0 {
					coords = append(coords, []float64{node.Lon, node.Lat})
				} else if nc, ok := nodeCoords.Load(int64(node.ID)); ok {
					coords = append(coords, nc.([]float64))
				}
			}
			if len(coords) < 2 {
				scannerSkipped++
				continue
			}
			item = &workItem{
				ID:     fmt.Sprintf("way/%d", int64(o.ID)),
				Type:   "way",
				Tags:   tags,
				Coords: coords,
			}

		case *osmapi.Relation:
			tags := o.TagMap()
			if len(tags) == 0 {
				scannerSkipped++
				continue
			}
			// Get first member node coordinate as representative point
			var coords [][]float64
			for _, member := range o.Members {
				if member.Type == osmapi.TypeNode {
					if nc, ok := nodeCoords.Load(member.Ref); ok {
						coords = [][]float64{nc.([]float64)}
						break
					}
				} else if member.Type == osmapi.TypeWay {
					if member.Lat != 0 || member.Lon != 0 {
						coords = [][]float64{{member.Lon, member.Lat}}
						break
					}
				}
			}
			if len(coords) == 0 {
				scannerSkipped++
				continue
			}
			item = &workItem{
				ID:     fmt.Sprintf("relation/%d", int64(o.ID)),
				Type:   "relation",
				Tags:   tags,
				Coords: coords,
			}
		}

		if item != nil {
			workChan <- item
		} else {
			scannerSkipped++
		}

		if totalScanned%logInterval == 0 {
			slog.Info(
				"indexing progress",
				"scanned",
				totalScanned,
				"skipped_by_scanner",
				scannerSkipped,
			)
		}
	}

	// Close work channel to signal workers to exit
	close(workChan)

	// Wait for workers to finish
	if err := g.Wait(); err != nil {
		cancel()
		// Drain result channel to unblock collector
		go func() { for range resultChan {} }()
		return err
	}

	// Close result channel to signal collector to exit
	close(resultChan)

	// Wait for collector to finish
	collectorWg.Wait()

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("PBF scan error: %w", err)
	}

	collectorMu.Lock()
	defer collectorMu.Unlock()

	if collectorErr != nil {
		return collectorErr
	}

	slog.Info("Finished!", "scanned", totalScanned, "skipped", scannerSkipped)
	return nil
}

// processWorker is a worker goroutine that processes entities and sends results to resultChan.
// Each worker has its own GEOS context for thread safety.
//
//nolint:funlen,cyclop // Worker processing requires many entity type and classification cases
func processWorker(
	ctx context.Context,
	workerID int,
	workChan <-chan *workItem,
	resultChan chan<- *processedItem,
	conf *config.Config,
	wdLookup *WikidataLookup,
	ont *PlaceTypeOntology,
	geosCtx *geos.Context,
) error {
	for {
		select {
		case <-ctx.Done():
			// Drain remaining work items to unblock producer
			go func() { for range workChan {} }()
			return ctx.Err()
		case item, ok := <-workChan:
			if !ok {
				return nil // Channel closed, worker exits
			}

			// Classify entity
			classifications := ClassifyMulti(item.Tags, &conf.Importance, ont)
			if len(classifications) == 0 {
				resultChan <- &processedItem{ID: item.ID} // Signal processed but skipped
				continue
			}

			// Check --only-named filter
			if conf.OnlyNamed {
				hasName := item.Tags["name"] != ""
				if !hasName {
					for _, alt := range []string{"alt_name", "old_name", "short_name"} {
						if item.Tags[alt] != "" {
							hasName = true
							break
						}
					}
					if !hasName {
						for _, lang := range conf.Languages {
							if item.Tags["name:"+lang] != "" {
								hasName = true
								break
							}
						}
					}
				}
				if !hasName {
					resultChan <- &processedItem{ID: item.ID} // Signal processed but skipped
					continue
				}
			}

			// Find best classification by importance with tie-breaking
			best := classifications[0]
			for _, c := range classifications[1:] {
				if c.Importance > best.Importance {
					best = c
				} else if c.Importance == best.Importance {
					if c.OntLevel > best.OntLevel {
						best = c
					} else if c.OntLevel == best.OntLevel {
						cPop := parsePopulation(item.Tags)
						bestPop := parsePopulationForClassification(best.Class, best.Subtype, item.Tags)
						if cPop > bestPop {
							best = c
						}
					}
				}
			}

			// Apply Wikidata importance boost (language-aware)
			if wdLookup != nil && item.Tags["wikidata"] != "" {
				primaryLang := ""
				if len(conf.Languages) > 0 {
					primaryLang = conf.Languages[0]
				}
				for _, lang := range conf.Languages {
					if _, ok := item.Tags["name:"+lang]; ok {
						primaryLang = lang
						break
					}
				}
				var wdImportance float64
				if primaryLang != "" {
					wdImportance = wdLookup.GetImportanceForLang(item.Tags["wikidata"], primaryLang)
				} else {
					wdImportance = wdLookup.GetImportance(item.Tags["wikidata"])
				}
				if wdImportance > 0 {
					best.Importance += wdImportance * 10.0
				}
			}

			// Build geometry
			var geom any
			geoMode := GeometryMode(conf.GeometryMode)
			if geoMode != ModeNoGeo {
				var err error
				geom, err = createGeometryFromCoords(item.Coords, geoMode, conf.SimplificationTol, geosCtx)
				if err != nil {
					slog.Debug("worker skipping entity (bad geometry)", "id", item.ID, "error", err)
					resultChan <- &processedItem{ID: item.ID} // Signal processed but skipped
					continue
				}
			}

			// Build feature
			feature := &search.Feature{
				ID:         item.ID,
				Name:       item.Tags["name"],
				Names:      make(map[string]string),
				Importance: best.Importance,
				Geometry:   geom,
				Class:      best.Class,
				Subtype:    best.Subtype,
			}

			if conf.DisableImportance {
				feature.Importance = 0
			}

			if !conf.DisableClassSubtype {
				classes := make([]string, len(classifications))
				subtypes := make([]string, len(classifications))
				for i, c := range classifications {
					classes[i] = c.Class
					subtypes[i] = c.Subtype
				}
				feature.Classes = classes
				feature.Subtypes = subtypes
			}

			if !conf.DisableAltNames {
				for _, alt := range []string{"alt_name", "old_name", "short_name"} {
					if val, ok := item.Tags[alt]; ok {
						feature.Names[alt] = val
					}
				}
				for _, lang := range conf.Languages {
					if name, ok := item.Tags["name:"+lang]; ok {
						feature.Names["name:"+lang] = name
					}
					for _, alt := range []string{"alt_name", "old_name", "short_name"} {
						if val, ok := item.Tags[alt+":"+lang]; ok {
							feature.Names[alt+":"+lang] = val
						}
					}
				}
			} else {
				for _, lang := range conf.Languages {
					if name, ok := item.Tags["name:"+lang]; ok {
						feature.Names["name:"+lang] = name
					}
				}
			}

			// Extract address fields when configured
			if conf.StoreAddress {
				feature.Address = make(map[string]string)
				addrKeys := []string{
					"addr:housenumber", "addr:street", "addr:city", "addr:postcode",
					"addr:country", "addr:state", "addr:district", "addr:suburb",
					"addr:neighbourhood",
				}
				for _, k := range addrKeys {
					if val, ok := item.Tags[k]; ok {
						feature.Address[k] = val
					}
				}
			}

			// Attach Wikidata redirect titles as alternate names when configured
			if conf.IndexWikidataRedirects && wdLookup != nil && item.Tags["wikidata"] != "" {
				redirects := wdLookup.GetRedirects(item.Tags["wikidata"])
				if len(redirects) > 0 {
					feature.WikidataRedirects = redirects
				}
			}

			// Send feature to collector (single index writer)
			resultChan <- &processedItem{
				ID:      item.ID,
				Feature: search.FeatureToMap(feature),
			}
		}
	}
}

func parseEntityID(id string) int64 {
	// Extract numeric ID from "type/id" format
	var n int64
	_, _ = fmt.Sscanf(id, "%*[^/]/%d", &n)
	return n
}

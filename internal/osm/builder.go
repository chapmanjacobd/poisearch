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
//nolint:revive,funlen
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

	wdLookup, ont := setupBuildIndex(conf)

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
			nodeCoords[int64(o.ID)] = []float64{o.Lon, o.Lat}

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
				} else if nc, ok := nodeCoords[int64(node.ID)]; ok {
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
	feature := &search.Feature{
		ID:         fmt.Sprintf("%s/%d", p.entityType, p.id),
		Name:       p.tags["name"],
		Names:      make(map[string]string),
		Importance: p.best.Importance,
		Geometry:   p.geom,
		Class:      p.best.Class,
		Subtype:    p.best.Subtype,
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
			"addr:neighbourhood",
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
func buildIndexParallel(inputPath string, conf *config.Config, index bleve.Index, workers int) error {
	slog.Info("building index with parallel workers", "path", conf.IndexPath, "input", inputPath, "workers", workers)

	wdLookup, ont := setupBuildIndex(conf)

	var nodeCoords sync.Map
	workChan := make(chan *workItem, workers*100)
	resultChan := make(chan *processedItem, workers*100)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	totalScanned, scannerSkipped, err := runProducer(inputPath, conf, &nodeCoords, workChan)
	close(workChan)

	if err != nil {
		return err
	}

	if err := g.Wait(); err != nil {
		cancel()
		drainResultChan(resultChan)
		return err
	}

	close(resultChan)
	collectorWg.Wait()

	if collectorErr != nil {
		return collectorErr
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

func drainResultChan(resultChan <-chan *processedItem) {
	go func() {
		for range resultChan {
			continue
		}
	}()
}

func runProducer(
	inputPath string,
	conf *config.Config,
	nodeCoords *sync.Map,
	workChan chan<- *workItem,
) (scanned, skipped int, err error) {
	file, err := os.Open(inputPath)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := osmpbf.New(context.Background(), file, runtime.GOMAXPROCS(-1))
	if conf.NodesOnly {
		scanner.SkipWays = true
		scanner.SkipRelations = true
	}
	defer scanner.Close()

	for scanner.Scan() {
		obj := scanner.Object()
		scanned++
		item := buildWorkItem(obj, nodeCoords)
		if item != nil {
			workChan <- item
		} else {
			skipped++
		}
	}
	return scanned, skipped, scanner.Err()
}

func buildWorkItem(obj osmapi.Object, nodeCoords *sync.Map) *workItem {
	switch o := obj.(type) {
	case *osmapi.Node:
		nodeCoords.Store(int64(o.ID), []float64{o.Lon, o.Lat})
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

func extractWayCoords(o *osmapi.Way, nodeCoords *sync.Map) [][]float64 {
	coords := make([][]float64, 0, len(o.Nodes))
	for _, node := range o.Nodes {
		if node.Lon != 0 || node.Lat != 0 {
			coords = append(coords, []float64{node.Lon, node.Lat})
		} else if nc, ok := nodeCoords.Load(int64(node.ID)); ok {
			if ncVal, ok := nc.([]float64); ok {
				coords = append(coords, ncVal)
			}
		}
	}
	return coords
}

func extractRelationCoords(o *osmapi.Relation, nodeCoords *sync.Map) [][]float64 {
	for _, member := range o.Members {
		switch member.Type {
		case osmapi.TypeNode:
			if nc, ok := nodeCoords.Load(member.Ref); ok {
				if ncVal, ok := nc.([]float64); ok {
					return [][]float64{ncVal}
				}
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

package osm

import (
	"errors"
	"fmt"
	"log/slog"
	"math"

	"github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
	"github.com/codesoap/pbf"
	"github.com/twpayne/go-geos"
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

// nanodegreeToFloat converts int64 nanodegrees to float64 coordinates
func nanodegreeToFloat(n int64) float64 {
	return float64(n) / 1_000_000_000.0
}

func truncate(f float64) float64 {
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
			"coordinates": []float64{truncate(g.X()), truncate(g.Y())},
		}
	case geos.TypeIDLineString, geos.TypeIDLinearRing:
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
	case geos.TypeIDMultiPoint:
		n := g.NumGeometries()
		coords := make([][]float64, 0, n)
		for i := range n {
			comp := g.Geometry(i)
			coords = append(coords, []float64{truncate(comp.X()), truncate(comp.Y())})
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
				lineCoords[j][0] = truncate(lineCoords[j][0])
				lineCoords[j][1] = truncate(lineCoords[j][1])
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
				ringCoords[j][0] = truncate(ringCoords[j][0])
				ringCoords[j][1] = truncate(ringCoords[j][1])
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
			"coordinates": []float64{truncate(centroid.X()), truncate(centroid.Y())},
		}
	}
}

// buildTagFilter creates a pbf.Filter from the config's importance weights.
// This enables early filtering at the PBF parsing level, skipping entities
// that have no classifiable tags before expensive operations like geometry
// creation and classification.
func buildTagFilter(weights *config.ImportanceWeights) pbf.Filter {
	filter := pbf.Filter{
		Tags: make(map[string][]string),
	}

	// Add tag keys to the filter.
	// If values is empty, we use an empty slice []string{} which means
	// "any value for this key, but key must be present".
	// If values has entries, we include those specific values.
	addTag := func(key string, values map[string]float64) {
		if len(values) == 0 {
			return
		}
		// If there are specific values configured, include them
		// Otherwise use empty slice to match any value for this key
		filter.Tags[key] = make([]string, 0, len(values))
		for v := range values {
			if v != "" && v != "yes" && v != "no" {
				filter.Tags[key] = append(filter.Tags[key], v)
			}
		}
		// If no specific values were added (all were yes/no/empty),
		// the slice is empty which means "any value for this key"
	}

	addTag("place", weights.Place)
	addTag("amenity", weights.Amenity)
	addTag("highway", weights.Highway)
	addTag("shop", weights.Shop)
	addTag("tourism", weights.Tourism)
	addTag("leisure", weights.Leisure)
	addTag("historic", weights.Historic)
	addTag("natural", weights.Natural)
	addTag("railway", weights.Railway)

	return filter
}

// BuildIndex builds a Bleve index from an OSM PBF file using the codesoap/pbf library.
// This leverages vtprotobuf optimization, object pooling, and parallel blob decoding
// for significantly better performance than standard PBF parsers.
//
//nolint:revive,funlen // Index building requires coordinating multiple components, complexity is inherent
func BuildIndex(inputPath string, conf *config.Config, index bleve.Index) error {
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

	// Build tag filter from config weights for early filtering
	filter := buildTagFilter(&conf.Importance)

	// Extract entities using codesoap/pbf (optimized with vtprotobuf + parallel decoding)
	entities, err := pbf.ExtractEntities(inputPath, filter)
	if err != nil {
		return fmt.Errorf("extracting entities from PBF: %w", err)
	}

	slog.Info("extracted entities",
		"nodes", len(entities.Nodes),
		"ways", len(entities.Ways),
		"relations", len(entities.Relations))

	geosCtx := geos.NewContext()
	count := 0
	skipped := 0
	totalEntities := len(entities.Nodes) + len(entities.Ways) + len(entities.Relations)
	batch := index.NewBatch()
	batchSize := 500 // Flush more frequently for lower peak memory and faster resume
	geoMode := GeometryMode(conf.GeometryMode)

	// processEntity is a helper function to index a single entity
	// Bleve uses upsert semantics: re-indexing an existing ID overwrites it,
	// so interrupted builds can be safely resumed by re-running.
	processEntity := func(entityType string, id int64, tags map[string]string, coords [][]float64) error {
		feature, shouldIndex, err := buildFeature(entityType, id, tags, coords, conf, wdLookup, ont, geosCtx, geoMode)
		if err != nil {
			slog.Debug("skipping "+entityType, "id", id, "error", err)
			skipped++
			return nil
		}
		if !shouldIndex {
			skipped++
			return nil
		}

		if err := batch.Index(feature.ID, search.FeatureToMap(feature)); err != nil {
			slog.Error("error indexing feature", "id", feature.ID, "error", err)
			return nil
		}

		count++
		if count%batchSize == 0 {
			if err := index.Batch(batch); err != nil {
				return err
			}
			batch = index.NewBatch()
			slog.Info(
				"indexing progress",
				"indexed",
				count,
				"total",
				totalEntities,
				"pct",
				fmt.Sprintf("%.1f%%", float64(count)*100/float64(totalEntities)),
			)
		}
		return nil
	}

	// Process nodes
	for _, node := range entities.Nodes {
		lat, lon := node.Coords()
		if err := processEntity("node", node.ID(), node.Tags(), [][]float64{{
			nanodegreeToFloat(lon),
			nanodegreeToFloat(lat),
		}}); err != nil {
			return err
		}
	}

	// Process ways
	if !conf.NodesOnly {
		for _, way := range entities.Ways {
			coords, err := wayCoordinates(way, entities.Nodes)
			if err != nil {
				slog.Debug("skipping way (no coordinates)", "id", way.ID(), "error", err)
				skipped++
				continue
			}

			if err := processEntity("way", way.ID(), way.Tags(), coords); err != nil {
				return err
			}
		}
	}

	// Process relations
	if !conf.NodesOnly {
		for _, relation := range entities.Relations {
			coords, err := relationCoordinates(relation, entities.Nodes)
			if err != nil {
				slog.Debug("skipping relation (no coordinates)", "id", relation.ID(), "error", err)
				skipped++
				continue
			}

			if err := processEntity("relation", relation.ID(), relation.Tags(), coords); err != nil {
				return err
			}
		}
	}

	// Flush remaining batch
	if batch.Size() > 0 {
		if err := index.Batch(batch); err != nil {
			return err
		}
	}

	slog.Info("Finished!", "indexed_features", count, "skipped", skipped)
	return nil
}

// buildFeature creates a search.Feature from OSM entity data.
//
//nolint:revive // Feature building requires handling many classification and geometry cases
func buildFeature(
	entityType string,
	id int64,
	tags map[string]string,
	coords [][]float64,
	conf *config.Config,
	wdLookup *WikidataLookup,
	ont *PlaceTypeOntology,
	geosCtx *geos.Context,
	geoMode GeometryMode,
) (
	*search.Feature,
	bool,
	error,
) {
	if len(tags) == 0 {
		return nil, false, nil
	}

	classifications := ClassifyMulti(tags, &conf.Importance, ont)
	if len(classifications) == 0 {
		return nil, false, nil
	}

	// Check --only-named filter
	if conf.OnlyNamed {
		hasName := tags["name"] != ""
		if !hasName {
			altNames := []string{"alt_name", "old_name", "short_name"}
			for _, alt := range altNames {
				if tags[alt] != "" {
					hasName = true
					break
				}
			}
			if !hasName {
				for _, lang := range conf.Languages {
					if tags["name:"+lang] != "" {
						hasName = true
						break
					}
				}
			}
		}
		if !hasName {
			return nil, false, nil
		}
	}

	// Use the highest-importance classification
	best := classifications[0]
	for _, c := range classifications[1:] {
		if c.Importance > best.Importance {
			best = c
		}
	}

	// Apply Wikidata importance boost
	if wdLookup != nil && tags["wikidata"] != "" {
		wdImportance := wdLookup.GetImportance(tags["wikidata"])
		if wdImportance > 0 {
			best.Importance += wdImportance * 10.0
		}
	}

	// Build geometry
	var geom any
	var err error
	if geoMode != ModeNoGeo {
		geom, err = createGeometryFromCoords(coords, geoMode, conf.SimplificationTol, geosCtx)
		if err != nil {
			return nil, false, fmt.Errorf("creating geometry: %w", err)
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
		altNames := []string{"alt_name", "old_name", "short_name"}
		for _, alt := range altNames {
			if val, ok := tags[alt]; ok {
				feature.Names[alt] = val
			}
		}

		for _, lang := range conf.Languages {
			if name, ok := tags["name:"+lang]; ok {
				feature.Names["name:"+lang] = name
			}
			for _, alt := range altNames {
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

	return feature, true, nil
}

// wayCoordinates reconstructs coordinates for a way from its node references
func wayCoordinates(way pbf.Way, nodes map[int64]pbf.Node) ([][]float64, error) {
	nodeIDs := way.Nodes()
	if len(nodeIDs) == 0 {
		return nil, errors.New("way has no nodes")
	}

	coords := make([][]float64, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		node, ok := nodes[nodeID]
		if !ok {
			slog.Debug("way references node not in PBF", "way_id", way.ID(), "node_id", nodeID)
			continue
		}
		lat, lon := node.Coords()
		coords = append(coords, []float64{
			nanodegreeToFloat(lon),
			nanodegreeToFloat(lat),
		})
	}

	if len(coords) < 2 {
		return nil, errors.New("way has fewer than 2 nodes with coordinates")
	}

	return coords, nil
}

// relationCoordinates gets a representative point from a relation's member nodes
func relationCoordinates(relation pbf.Relation, nodes map[int64]pbf.Node) ([][]float64, error) {
	for _, nodeID := range relation.Nodes() {
		node, ok := nodes[nodeID]
		if !ok {
			continue
		}
		lat, lon := node.Coords()
		return [][]float64{{
			nanodegreeToFloat(lon),
			nanodegreeToFloat(lat),
		}}, nil
	}

	return nil, errors.New("relation has no member nodes with coordinates")
}

// createGeometryFromCoords creates a geometry from coordinates based on the geometry mode
func createGeometryFromCoords(
	coords [][]float64,
	mode GeometryMode,
	tolerance float64,
	ctx *geos.Context,
) (
	any,
	error,
) {
	if len(coords) == 0 {
		return nil, errors.New("no coordinates provided")
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
			"lat": truncate(pt.Y()),
			"lon": truncate(pt.X()),
		}, nil

	case ModeGeoshapeBBox:
		g = g.Envelope()
	case ModeGeoshapeSimplify:
		g = g.TopologyPreserveSimplify(tolerance)
	case ModeGeoshapeFull:
		// Use geometry as-is
	case ModeNoGeo:
		// Should not reach here since caller checks mode != ModeNoGeo
		return nil, errors.New("unexpected ModeNoGeo in createGeometryFromCoords")
	default:
		pt, _, err := representativePoint(g)
		if err != nil || pt == nil {
			return nil, fmt.Errorf("could not create default geopoint: %w", err)
		}
		return map[string]any{
			"lat": truncate(pt.Y()),
			"lon": truncate(pt.X()),
		}, nil
	}

	return geomToGeoJSON(g), nil
}

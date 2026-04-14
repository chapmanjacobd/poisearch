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

	"github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
	osmapi "github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
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

func roundToMeterAccuracy(f float64) float64 {
	shift := math.Pow(10, 5)
	return math.Floor(f*shift+0.5) / shift
}

func representativePoint(g *geos.Geom, ctx *geos.Context) (*geos.Geom, bool, error) {
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

// nameSignature creates a normalized name string for deduplication.
// POIs with identical names and similar classes in close proximity are likely duplicates.
func nameSignature(tags map[string]string, classifications []*Classification) string {
	name := strings.ToLower(tags["name"])
	if name == "" {
		// Try alternate names
		for _, alt := range []string{"alt_name", "old_name", "short_name"} {
			if v := tags[alt]; v != "" {
				name = strings.ToLower(v)
				break
			}
		}
	}
	if name == "" || len(classifications) == 0 {
		return ""
	}
	// Combine name with primary class for more accurate dedup
	return name + "|" + classifications[0].Class
}

// shouldDedup checks if an entity should be deduplicated.
// Returns true if the entity has been seen before with similar attributes.
// We track by name+class signature and spatial proximity to catch real-world duplicates.
type dedupKey struct {
	nameClass string
	latBucket int64 // coordinate bucket for spatial proximity check
	lonBucket int64
}

// BuildIndex builds a Bleve index from an OSM PBF file using paulmach/osm streaming scanner.
// This approach has lower memory than bulk-extraction libraries and supports parallel decoders.
//
// On-the-fly deduplication: POIs with identical name+class within ~50m are treated as duplicates.
// Only the highest-importance instance is kept.
//
//nolint:funlen,cyclop // Index building requires coordinating streaming, classification, geometry, and dedup
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

	// Build node lookup for ways/relations (needed for geometry reconstruction)
	// Streaming means we see nodes first, then ways/relations can reference them.
	// For large files this uses memory, but it's necessary for geometry.
	nodeCoords := make(map[int64][]float64)

	geosCtx := geos.NewContext()
	count := 0
	skipped := 0
	deduped := 0
	geoMode := GeometryMode(conf.GeometryMode)

	// Dedup tracking: key = name+class signature + spatial bucket, value = best importance seen
	// Bucket size is ~0.0005 degrees (~50m at equator)
	const bucketSize = 0.0005
	seen := make(map[dedupKey]float64)

	isDuplicate := func(sig string, lat, lon, importance float64) bool {
		if sig == "" {
			return false
		}
		latBucket := int64(lat / bucketSize)
		lonBucket := int64(lon / bucketSize)
		key := dedupKey{nameClass: sig, latBucket: latBucket, lonBucket: lonBucket}

		if prevImportance, exists := seen[key]; exists {
			// Already seen something similar; keep the highest importance
			if importance <= prevImportance {
				return true
			}
			// This one is better, replace
			seen[key] = importance
			return false
		}
		seen[key] = importance
		return false
	}

	batch := index.NewBatch()
	batchSize := 500

	processEntity := func(
		entityType string,
		id int64,
		tags map[string]string,
		coords [][]float64,
	) error {
		if len(tags) == 0 {
			skipped++
			return nil
		}

		classifications := ClassifyMulti(tags, &conf.Importance, ont)
		if len(classifications) == 0 {
			skipped++
			return nil
		}

		// Check --only-named filter
		if conf.OnlyNamed {
			hasName := tags["name"] != ""
			if !hasName {
				for _, alt := range []string{"alt_name", "old_name", "short_name"} {
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
				skipped++
				return nil
			}
		}

		// Find best classification by importance
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

		// Get a representative coordinate for dedup
		var repLat, repLon float64
		if len(coords) > 0 {
			repLat = coords[0][1]
			repLon = coords[0][0]
		}

		// Check for duplicates
		sig := nameSignature(tags, classifications)
		if isDuplicate(sig, repLat, repLon, best.Importance) {
			deduped++
			return nil
		}

		// Build geometry
		var geom any
		if geoMode != ModeNoGeo {
			var err error
			geom, err = createGeometryFromCoords(coords, geoMode, conf.SimplificationTol, geosCtx)
			if err != nil {
				slog.Debug("skipping entity (bad geometry)", "id", id, "error", err)
				skipped++
				return nil
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
			for _, alt := range []string{"alt_name", "old_name", "short_name"} {
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
		}
		return nil
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

		switch o := obj.(type) {
		case *osmapi.Node:
			// Store node coordinates for way lookup
			nodeCoords[int64(o.ID)] = []float64{o.Lon, o.Lat}

			tags := o.TagMap()
			coords := [][]float64{{o.Lon, o.Lat}}
			if err := processEntity("node", int64(o.ID), tags, coords); err != nil {
				return err
			}

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
			if err := processEntity("way", int64(o.ID), tags, coords); err != nil {
				return err
			}

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
			if err := processEntity("relation", int64(o.ID), tags, coords); err != nil {
				return err
			}
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
				"deduped",
				deduped,
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

	slog.Info("Finished!", "indexed", count, "skipped", skipped, "deduped", deduped)
	return nil
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
		if o.Bounds != nil {
			g = ctx.NewGeomFromBounds(o.Bounds.MinLon, o.Bounds.MinLat, o.Bounds.MaxLon, o.Bounds.MaxLat)
		} else {
			return nil, errors.New("relation without bounds")
		}
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
			pt, _, err = representativePoint(g, ctx)
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
	default:
		pt, _, err := representativePoint(g, ctx)
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
			pt, _, err = representativePoint(g, ctx)
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
	default:
		pt, _, err := representativePoint(g, ctx)
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

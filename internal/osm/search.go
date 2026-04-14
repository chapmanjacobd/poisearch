package osm

import (
	"fmt"
	"math"
	"strings"

	"github.com/blevesearch/bleve/v2"
	bleveSearch "github.com/blevesearch/bleve/v2/search"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
	"github.com/codesoap/pbf"
	"github.com/twpayne/go-geos"
)

// nanodegree converts a float64 coordinate to int64 nanodegrees
func nanodegree(f float64) int64 {
	return int64(1_000_000_000 * f)
}

// computeDistanceMeters returns the approximate distance in meters between two points
func computeDistanceMeters(lat1, lon1, lat2, lon2 float64) int {
	latRad := lat1 * math.Pi / 180
	y := (lat2 - lat1) * 111_000
	x := (lon2 - lon1) * 6_367_000 * math.Cos(latRad) * math.Pi / 180
	return int(math.Sqrt(float64(y*y + x*x)))
}

// parseRadiusToInt extracts the numeric meter value from a radius string like "1000m"
func parseRadiusToInt(radius string) int {
	radius = strings.TrimSuffix(radius, "m")
	radius = strings.TrimSuffix(radius, "M")
	var result int
	_, _ = fmt.Sscanf(radius, "%d", &result)
	return result
}

// PBFSearch performs direct PBF search without using a pre-built index
// Uses codesoap/pbf library for efficient entity extraction with vtprotobuf optimizations
//
//nolint:revive,cyclop,funlen // Search requires handling many spatial filtering and classification cases
func PBFSearch(pbfPath string, params search.SearchParams, conf *config.Config) (*bleve.SearchResult, error) {
	// Build tag filter for early filtering
	filter := buildTagFilter(&conf.Importance)

	// Extract entities using optimized pbf library
	entities, err := pbf.ExtractEntities(pbfPath, filter)
	if err != nil {
		return nil, fmt.Errorf("extracting entities from PBF: %w", err)
	}

	// Load ontology for classification
	ont := DefaultOntology()
	if conf.OntologyPath != "" {
		if loaded, err := LoadOntologyFromCSV(conf.OntologyPath); err == nil {
			ont = loaded
		}
	}

	geosCtx := geos.NewContext()
	queryLower := strings.ToLower(params.Query)

	// Pre-compute spatial filter parameters
	var radiusMeters int
	var minLatNano, maxLatNano, minLonNano, maxLonNano int64
	hasRadiusFilter := params.Lat != nil && params.Lon != nil && params.Radius != ""
	hasBboxFilter := params.MinLat != nil && params.MaxLat != nil && params.MinLon != nil && params.MaxLon != nil

	if hasRadiusFilter {
		radiusMeters = parseRadiusToInt(params.Radius)
		latCenter := nanodegree(*params.Lat)
		lonCenter := nanodegree(*params.Lon)

		radiusLatNano := int64(1_000_000_000 * float64(radiusMeters) / 111_000)
		radiusLonNano := int64(
			1_000_000_000 * float64(radiusMeters) / (6_367_000 * math.Cos(*params.Lat*math.Pi/180) * math.Pi / 180),
		)

		minLatNano = latCenter - radiusLatNano
		maxLatNano = latCenter + radiusLatNano
		minLonNano = lonCenter - radiusLonNano
		maxLonNano = lonCenter + radiusLonNano
	}

	if hasBboxFilter {
		minLatNano = nanodegree(*params.MinLat)
		maxLatNano = nanodegree(*params.MaxLat)
		minLonNano = nanodegree(*params.MinLon)
		maxLonNano = nanodegree(*params.MaxLon)
	}

	res := &bleve.SearchResult{
		Hits: make(bleveSearch.DocumentMatchCollection, 0),
	}

	// Search through nodes
	for _, node := range entities.Nodes {
		latNano, lonNano := node.Coords()

		if !matchesSpatialFilter(
			latNano,
			lonNano,
			hasRadiusFilter,
			hasBboxFilter,
			minLatNano,
			maxLatNano,
			minLonNano,
			maxLonNano,
			params,
			radiusMeters,
		) {

			continue
		}

		coords := [][]float64{{
			nanodegreeToFloat(lonNano),
			nanodegreeToFloat(latNano),
		}}

		if hit := processEntity(
			"node",
			node.ID(),
			node.Tags(),
			coords,
			queryLower,
			params,
			conf,
			ont,
			geosCtx,
			hasRadiusFilter,
			hasBboxFilter,
		); hit != nil {
			res.Hits = append(res.Hits, hit)
			res.Total++
			if len(res.Hits) >= params.Limit && params.Limit > 0 {
				break
			}
		}
	}

	// Search through ways
	if !conf.NodesOnly {
		for _, way := range entities.Ways {
			coords, err := wayCoordinates(way, entities.Nodes)
			if err != nil || len(coords) == 0 {
				continue
			}

			// Use first coordinate for spatial filtering
			firstLat := nanodegree(coords[0][1])
			firstLon := nanodegree(coords[0][0])

			if !matchesSpatialFilter(
				firstLat,
				firstLon,
				hasRadiusFilter,
				hasBboxFilter,
				minLatNano,
				maxLatNano,
				minLonNano,
				maxLonNano,
				params,
				radiusMeters,
			) {

				continue
			}

			if hit := processEntity(
				"way",
				way.ID(),
				way.Tags(),
				coords,
				queryLower,
				params,
				conf,
				ont,
				geosCtx,
				hasRadiusFilter,
				hasBboxFilter,
			); hit != nil {
				res.Hits = append(res.Hits, hit)
				res.Total++
				if len(res.Hits) >= params.Limit && params.Limit > 0 {
					break
				}
			}
		}
	}

	// Search through relations
	if !conf.NodesOnly {
		for _, relation := range entities.Relations {
			coords, err := relationCoordinates(relation, entities.Nodes)
			if err != nil || len(coords) == 0 {
				continue
			}

			firstLat := nanodegree(coords[0][1])
			firstLon := nanodegree(coords[0][0])

			if !matchesSpatialFilter(
				firstLat,
				firstLon,
				hasRadiusFilter,
				hasBboxFilter,
				minLatNano,
				maxLatNano,
				minLonNano,
				maxLonNano,
				params,
				radiusMeters,
			) {

				continue
			}

			if hit := processEntity(
				"relation",
				relation.ID(),
				relation.Tags(),
				coords,
				queryLower,
				params,
				conf,
				ont,
				geosCtx,
				hasRadiusFilter,
				hasBboxFilter,
			); hit != nil {
				res.Hits = append(res.Hits, hit)
				res.Total++
				if len(res.Hits) >= params.Limit && params.Limit > 0 {
					break
				}
			}
		}
	}

	return res, nil
}

// matchesSpatialFilter checks if coordinates match the spatial filter (radius or bbox)
//
//nolint:revive // Spatial filtering requires many parameters for efficient radius and bbox checks
func matchesSpatialFilter(
	latNano, lonNano int64,
	hasRadiusFilter, hasBboxFilter bool,
	minLatNano, maxLatNano, minLonNano, maxLonNano int64,
	params search.SearchParams,
	radiusMeters int,
) bool {
	if !hasRadiusFilter && !hasBboxFilter {
		return true
	}

	// Coarse bbox check
	if latNano < minLatNano || latNano > maxLatNano ||
		lonNano < minLonNano || lonNano > maxLonNano {

		return false
	}

	// Precise radius check
	if hasRadiusFilter {
		dist := computeDistanceMeters(
			float64(latNano)/1_000_000_000,
			float64(lonNano)/1_000_000_000,
			*params.Lat,
			*params.Lon,
		)
		if dist > radiusMeters {
			return false
		}
	}

	return true
}

// processEntity processes an OSM entity and creates a search result match.
//
//nolint:revive // Processing entities requires handling many classification and geometry cases with multiple parameters
func processEntity(
	entityType string,
	id int64,
	tags map[string]string,
	coords [][]float64,
	queryLower string,
	params search.SearchParams,
	conf *config.Config,
	ont *PlaceTypeOntology,
	geosCtx *geos.Context,
	hasRadiusFilter, hasBboxFilter bool,
) *bleveSearch.DocumentMatch {
	classifications := ClassifyMulti(tags, &conf.Importance, ont)
	if len(classifications) == 0 {
		return nil
	}

	// Check class/subtype filter
	classMatched := params.Class == ""
	subtypeMatched := params.Subtype == ""

	if !classMatched {
		for _, c := range classifications {
			if c.Class == params.Class {
				classMatched = true
				break
			}
		}
	}
	if !subtypeMatched {
		for _, c := range classifications {
			if c.Subtype == params.Subtype {
				subtypeMatched = true
				break
			}
		}
	}
	if !classMatched || !subtypeMatched {
		return nil
	}

	// Name match
	matched := queryLower == ""
	if !matched {
		for k, v := range tags {
			if strings.HasPrefix(k, "name") || strings.HasPrefix(k, "alt_name") || strings.HasPrefix(k, "short_name") {
				if strings.Contains(strings.ToLower(v), queryLower) {
					matched = true
					break
				}
			}
		}
	}
	if !matched {
		return nil
	}

	best := classifications[0]
	for _, c := range classifications[1:] {
		if c.Importance > best.Importance {
			best = c
		}
	}

	// Build geometry if needed
	var geom any
	var distMeters int
	if hasRadiusFilter && len(coords) > 0 {
		distMeters = computeDistanceMeters(*params.Lat, *params.Lon, coords[0][1], coords[0][0])
		g, err := createGeometryFromCoords(coords, ModeGeopoint, 0, geosCtx)
		if err == nil {
			geom = g
		}
	} else if hasBboxFilter {
		g, err := createGeometryFromCoords(coords, ModeGeopoint, 0, geosCtx)
		if err == nil {
			geom = g
		}
	}

	hit := &bleveSearch.DocumentMatch{
		ID:    fmt.Sprintf("%s/%d", entityType, id),
		Score: best.Importance,
		Fields: map[string]any{
			"name":     tags["name"],
			"class":    best.Class,
			"subtype":  best.Subtype,
			"geometry": geom,
		},
	}
	if hasRadiusFilter && distMeters > 0 {
		hit.Score = float64(distMeters)
	}

	return hit
}

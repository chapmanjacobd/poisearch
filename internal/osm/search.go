package osm

import (
	"context"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"

	"github.com/blevesearch/bleve/v2"
	bleveSearch "github.com/blevesearch/bleve/v2/search"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
	osmapi "github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
	"github.com/twpayne/go-geos"
)

// nanodegree converts a float64 coordinate to int64 nanodegrees
func nanodegree(f float64) int64 {
	return int64(1_000_000_000 * f)
}

// computeDistanceMeters returns the approximate distance in meters between two points
func computeDistanceMeters(lat1, lon1, lat2, lon2 float64) int {
	y := (lat2 - lat1) * 111_000
	x := (lon2 - lon1) * 6_367_000 * math.Cos(lat1*math.Pi/180) * math.Pi / 180
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

// PBFSearch performs direct PBF search without using a pre-built index.
// Uses paulmach/osm streaming scanner for low-memory, fast parsing.
//
//nolint:funlen,cyclop // Search requires handling many spatial filtering and classification cases
func PBFSearch(pbfPath string, params search.SearchParams, conf *config.Config) (*bleve.SearchResult, error) {
	// Load ontology for classification
	ont := DefaultOntology()
	if conf.OntologyPath != "" {
		if loaded, err := LoadOntologyFromCSV(conf.OntologyPath); err == nil {
			ont = loaded
		}
	}

	file, err := os.Open(pbfPath)
	if err != nil {
		return nil, fmt.Errorf("opening PBF file: %w", err)
	}
	defer file.Close()

	scanner := osmpbf.New(context.Background(), file, runtime.GOMAXPROCS(-1))
	defer scanner.Close()

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

	// Node coordinate lookup for way/relation geometry reconstruction
	nodeCoords := make(map[int64][]float64)

	for scanner.Scan() {
		obj := scanner.Object()

		switch o := obj.(type) {
		case *osmapi.Node:
			nodeCoords[int64(o.ID)] = []float64{o.Lon, o.Lat}
			latNano := nanodegree(o.Lat)
			lonNano := nanodegree(o.Lon)

			if !matchesSpatialFilter(latNano, lonNano, hasRadiusFilter, hasBboxFilter,
				minLatNano, maxLatNano, minLonNano, maxLonNano, params, radiusMeters) {

				continue
			}

			if hit := processPBFFntity(
				"node", int64(o.ID), o.TagMap(), [][]float64{{o.Lon, o.Lat}},
				queryLower, params, conf, ont, geosCtx, hasRadiusFilter, hasBboxFilter,
			); hit != nil {
				res.Total++
				if params.From > 0 && int64(res.Total) <= int64(params.From) {
					continue
				}
				res.Hits = append(res.Hits, hit)
				if len(res.Hits) >= params.Limit && params.Limit > 0 {
					return res, nil
				}
			}

		case *osmapi.Way:
			if conf.NodesOnly {
				continue
			}
			coords := make([][]float64, 0, len(o.Nodes))
			for _, node := range o.Nodes {
				if node.Lon != 0 || node.Lat != 0 {
					coords = append(coords, []float64{node.Lon, node.Lat})
				} else if nc, ok := nodeCoords[int64(node.ID)]; ok {
					coords = append(coords, nc)
				}
			}
			if len(coords) < 2 {
				continue
			}

			firstLat := nanodegree(coords[0][1])
			firstLon := nanodegree(coords[0][0])

			if !matchesSpatialFilter(firstLat, firstLon, hasRadiusFilter, hasBboxFilter,
				minLatNano, maxLatNano, minLonNano, maxLonNano, params, radiusMeters) {

				continue
			}

			if hit := processPBFFntity(
				"way", int64(o.ID), o.TagMap(), coords,
				queryLower, params, conf, ont, geosCtx, hasRadiusFilter, hasBboxFilter,
			); hit != nil {
				res.Total++
				if params.From > 0 && int64(res.Total) <= int64(params.From) {
					continue
				}
				res.Hits = append(res.Hits, hit)
				if len(res.Hits) >= params.Limit && params.Limit > 0 {
					return res, nil
				}
			}

		case *osmapi.Relation:
			if conf.NodesOnly {
				continue
			}
			var coords [][]float64
			for _, member := range o.Members {
				if member.Type == osmapi.TypeNode {
					if nc, ok := nodeCoords[member.Ref]; ok {
						coords = [][]float64{nc}
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
				continue
			}

			firstLat := nanodegree(coords[0][1])
			firstLon := nanodegree(coords[0][0])

			if !matchesSpatialFilter(firstLat, firstLon, hasRadiusFilter, hasBboxFilter,
				minLatNano, maxLatNano, minLonNano, maxLonNano, params, radiusMeters) {

				continue
			}

			if hit := processPBFFntity(
				"relation", int64(o.ID), o.TagMap(), coords,
				queryLower, params, conf, ont, geosCtx, hasRadiusFilter, hasBboxFilter,
			); hit != nil {
				res.Total++
				if params.From > 0 && int64(res.Total) <= int64(params.From) {
					continue
				}
				res.Hits = append(res.Hits, hit)
				if len(res.Hits) >= params.Limit && params.Limit > 0 {
					return res, nil
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("PBF scan error: %w", err)
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

// processPBFFntity processes an OSM entity and creates a search result match.
//
//nolint:revive // Processing entities requires handling many classification and geometry cases with multiple parameters
func processPBFFntity(
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
		// Combine importance with distance decay: importance * (1 / (1 + distance_km))
		// This preserves importance ranking while penalizing distant results
		hit.Score = best.Importance * (1.0 / (1.0 + float64(distMeters)/1000.0))
		hit.Fields["distance_meters"] = distMeters
	}

	return hit
}

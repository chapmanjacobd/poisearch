package osm

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/blevesearch/bleve/v2"
	bleveSearch "github.com/blevesearch/bleve/v2/search"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
	"github.com/twpayne/go-geos"
)

// nanodegree converts a float64 coordinate to int64 nanodegrees.
// This avoids floating-point comparison issues and enables efficient integer arithmetic.
func nanodegree(f float64) int64 {
	return int64(1_000_000_000 * f)
}

// computeDistanceMeters returns the approximate distance in meters between two
// points using a simple Euclidean approximation. Accurate enough for small radii.
func computeDistanceMeters(lat1, lon1, lat2, lon2 float64) int {
	latRad := lat1 * math.Pi / 180
	y := (lat2 - lat1) * 111_000
	x := (lon2 - lon1) * 6_367_000 * math.Cos(latRad) * math.Pi / 180
	return int(math.Sqrt(float64(y*y + x*x)))
}

// parseRadiusToInt extracts the numeric meter value from a radius string like "1000m".
func parseRadiusToInt(radius string) int {
	radius = strings.TrimSuffix(radius, "m")
	radius = strings.TrimSuffix(radius, "M")
	var result int
	fmt.Sscanf(radius, "%d", &result)
	return result
}

func PBFSearch(pbfPath string, params search.SearchParams, conf *config.Config) (*bleve.SearchResult, error) {
	f, err := os.Open(pbfPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Use parallel scanner (Optimization #1)
	scanner := osmpbf.New(context.Background(), f, 4)
	defer scanner.Close()

	res := &bleve.SearchResult{
		Hits: make(bleveSearch.DocumentMatchCollection, 0),
	}

	geosCtx := geos.NewContext()
	queryLower := strings.ToLower(params.Query)

	// Load ontology for classification
	ont := DefaultOntology()
	if conf.OntologyPath != "" {
		if loaded, err := LoadOntologyFromCSV(conf.OntologyPath); err == nil {
			ont = loaded
		}
	}

	// Pre-compute spatial filter parameters (Optimization: two-phase filtering)
	var radiusMeters int
	var minLatNano, maxLatNano, minLonNano, maxLonNano int64
	hasRadiusFilter := params.Lat != nil && params.Lon != nil && params.Radius != ""
	if hasRadiusFilter {
		radiusMeters = parseRadiusToInt(params.Radius)

		// Coarse square bounding box pre-filter (from osmar)
		latCenter := nanodegree(*params.Lat)
		lonCenter := nanodegree(*params.Lon)

		// 1 degree latitude ≈ 111,000 meters
		// 1 degree longitude ≈ 111,000 * cos(lat) meters
		radiusLatNano := int64(1_000_000_000 * float64(radiusMeters) / 111_000)
		radiusLonNano := int64(1_000_000_000 * float64(radiusMeters) / (6_367_000 * math.Cos(*params.Lat*math.Pi/180) * math.Pi / 180))

		minLatNano = latCenter - radiusLatNano
		maxLatNano = latCenter + radiusLatNano
		minLonNano = lonCenter - radiusLonNano
		maxLonNano = lonCenter + radiusLonNano
	}

	// Parse bbox filter
	hasBboxFilter := params.MinLat != nil && params.MaxLat != nil && params.MinLon != nil && params.MaxLon != nil
	if hasBboxFilter {
		minLatNano = nanodegree(*params.MinLat)
		maxLatNano = nanodegree(*params.MaxLat)
		minLonNano = nanodegree(*params.MinLon)
		maxLonNano = nanodegree(*params.MaxLon)
	}

	for scanner.Scan() {
		obj := scanner.Object()

		// Phase 1: Coarse spatial pre-filter (before tag parsing)
		if hasRadiusFilter || hasBboxFilter {
			var lat, lon float64
			switch o := obj.(type) {
			case *osm.Node:
				lat = o.Lat
				lon = o.Lon
			case *osm.Way:
				if len(o.Nodes) > 0 {
					lat = o.Nodes[0].Lat
					lon = o.Nodes[0].Lon
				}
			default:
				// Relations: skip spatial pre-filter, check later
			}

			if lat != 0 || lon != 0 {
				latNano := nanodegree(lat)
				lonNano := nanodegree(lon)

				// Coarse bbox check using integer arithmetic
				if latNano < minLatNano || latNano > maxLatNano ||
					lonNano < minLonNano || lonNano > maxLonNano {
					continue // Outside bounding box, skip entirely
				}
			}
		}

		var tags map[string]string
		var id int64

		switch o := obj.(type) {
		case *osm.Node:
			tags = o.TagMap()
			id = int64(o.ID)
		case *osm.Way:
			if conf.NodesOnly {
				continue
			}
			tags = o.TagMap()
			id = int64(o.ID)
		case *osm.Relation:
			if conf.NodesOnly {
				continue
			}
			tags = o.TagMap()
			id = int64(o.ID)
		default:
			continue
		}

		// 1. Category Filter
		classifications := ClassifyMulti(tags, &conf.Importance, ont)
		if len(classifications) == 0 {
			continue
		}

		// Check if any classification matches the class filter
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
			continue
		}

		// Use the highest-importance classification for the hit
		best := classifications[0]
		for _, c := range classifications[1:] {
			if c.Importance > best.Importance {
				best = c
			}
		}

		// 2. Name Match
		matched := false
		if queryLower == "" {
			matched = true
		} else {
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
			continue
		}

		// 3. Spatial Filter - Phase 2: Precise radius check
		var geom any
		var distMeters int
		if hasRadiusFilter {
			var lat, lon float64
			switch o := obj.(type) {
			case *osm.Node:
				lat = o.Lat
				lon = o.Lon
			case *osm.Way:
				if len(o.Nodes) > 0 {
					lat = o.Nodes[0].Lat
					lon = o.Nodes[0].Lon
				}
			default:
				// For relations, try to get geometry later
			}

			if lat != 0 || lon != 0 {
				distMeters = computeDistanceMeters(*params.Lat, *params.Lon, lat, lon)
				if distMeters > radiusMeters {
					continue // Outside precise radius
				}

				g, err := CreateGeometry(obj, ModeGeopoint, 0, geosCtx)
				if err == nil {
					geom = g
				}
			}
		} else if hasBboxFilter {
			g, err := CreateGeometry(obj, ModeGeopoint, 0, geosCtx)
			if err != nil {
				continue
			}
			geom = g
		}

		hit := &bleveSearch.DocumentMatch{
			ID:    fmt.Sprintf("%s/%d", obj.ObjectID().Type(), id),
			Score: best.Importance,
			Fields: map[string]interface{}{
				"name":     tags["name"],
				"class":    best.Class,
				"subtype":  best.Subtype,
				"geometry": geom,
			},
		}
		// Store distance in score for radius searches
		if hasRadiusFilter && distMeters > 0 {
			hit.Score = float64(distMeters)
		}
		res.Hits = append(res.Hits, hit)
		res.Total++

		if len(res.Hits) >= params.Limit && params.Limit > 0 {
			break
		}
	}

	return res, scanner.Err()
}

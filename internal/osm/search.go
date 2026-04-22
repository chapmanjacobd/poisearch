package osm

import (
	"context"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/blevesearch/bleve/v2"
	bleveSearch "github.com/blevesearch/bleve/v2/search"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
	osmapi "github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
	"github.com/twpayne/go-geos"
)

type spatialFilter struct {
	hasRadius, hasBbox             bool
	radiusMeters                   int
	minLat, maxLat, minLon, maxLon int64
}

// PBFSearch performs a search directly on an OSM PBF file without using a Bleve index.
// This is slower but useful for small PBF files or debugging.
//
//nolint:revive // Direct PBF searching requires coordinating streaming and filtering
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

	if params.Limit <= 0 {
		params.Limit = 100
	}
	if params.Limit > 1000 {
		params.Limit = 1000
	}

	geosCtx := geos.NewContext()
	queryLower := strings.ToLower(params.Query)

	filter := computeSpatialFilter(params)

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

			if !matchesSpatialFilter(latNano, lonNano, filter.hasRadius, filter.hasBbox,
				filter.minLat, filter.maxLat, filter.minLon, filter.maxLon, params, filter.radiusMeters) {

				continue
			}

			if hit := processPBFEntity("node", int64(o.ID), o.TagMap(), [][]float64{{o.Lon, o.Lat}},
				queryLower, params, conf, ont, geosCtx); hit != nil {
				collectHit(res, hit)
			}

		case *osmapi.Way:
			if conf.NodesOnly {
				continue
			}
			coords := wayToCoords(o, nodeCoords)
			if len(coords) < 2 {
				continue
			}

			if !matchesSpatialFilter(
				nanodegree(coords[0][1]),
				nanodegree(coords[0][0]),
				filter.hasRadius,
				filter.hasBbox,
				filter.minLat,
				filter.maxLat,
				filter.minLon,
				filter.maxLon,
				params,
				filter.radiusMeters,
			) {

				continue
			}

			if hit := processPBFEntity("way", int64(o.ID), o.TagMap(), coords,
				queryLower, params, conf, ont, geosCtx); hit != nil {
				collectHit(res, hit)
			}

		case *osmapi.Relation:
			if conf.NodesOnly {
				continue
			}
			coords := relationToCoords(o, nodeCoords)
			if len(coords) == 0 {
				continue
			}

			if !matchesSpatialFilter(
				nanodegree(coords[0][1]),
				nanodegree(coords[0][0]),
				filter.hasRadius,
				filter.hasBbox,
				filter.minLat,
				filter.maxLat,
				filter.minLon,
				filter.maxLon,
				params,
				filter.radiusMeters,
			) {

				continue
			}

			if hit := processPBFEntity("relation", int64(o.ID), o.TagMap(), coords,
				queryLower, params, conf, ont, geosCtx); hit != nil {
				collectHit(res, hit)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("PBF scan error: %w", err)
	}

	return sortAndTruncateDirectHits(res, params.From, params.Limit), nil
}

func computeSpatialFilter(params search.SearchParams) spatialFilter {
	var f spatialFilter
	f.hasRadius = params.Lat != nil && params.Lon != nil && params.Radius != ""
	f.hasBbox = params.MinLat != nil && params.MaxLat != nil && params.MinLon != nil && params.MaxLon != nil

	if f.hasRadius {
		f.radiusMeters = parseRadiusToInt(params.Radius)
		latCenter := nanodegree(*params.Lat)
		lonCenter := nanodegree(*params.Lon)

		radiusLatNano := int64(1_000_000_000 * float64(f.radiusMeters) / 111_000)
		radiusLonNano := int64(
			1_000_000_000 * float64(f.radiusMeters) / (6_367_000 * math.Cos(*params.Lat*math.Pi/180) * math.Pi / 180),
		)

		f.minLat = latCenter - radiusLatNano
		f.maxLat = latCenter + radiusLatNano
		f.minLon = lonCenter - radiusLonNano
		f.maxLon = lonCenter + radiusLonNano
	}

	if f.hasBbox {
		f.minLat = nanodegree(*params.MinLat)
		f.maxLat = nanodegree(*params.MaxLat)
		f.minLon = nanodegree(*params.MinLon)
		f.maxLon = nanodegree(*params.MaxLon)
	}
	return f
}

func collectHit(res *bleve.SearchResult, hit *bleveSearch.DocumentMatch) {
	res.Total++
	res.Hits = append(res.Hits, hit)
}

func wayToCoords(o *osmapi.Way, nodeCoords map[int64][]float64) [][]float64 {
	coords := make([][]float64, 0, len(o.Nodes))
	for _, node := range o.Nodes {
		if node.Lon != 0 || node.Lat != 0 {
			coords = append(coords, []float64{node.Lon, node.Lat})
		} else if nc, ok := nodeCoords[int64(node.ID)]; ok {
			coords = append(coords, nc)
		}
	}
	return coords
}

func relationToCoords(o *osmapi.Relation, nodeCoords map[int64][]float64) [][]float64 {
	for _, member := range o.Members {
		switch member.Type {
		case osmapi.TypeNode:
			if nc, ok := nodeCoords[member.Ref]; ok {
				return [][]float64{nc}
			}
		case osmapi.TypeWay:
			if member.Lat != 0 || member.Lon != 0 {
				return [][]float64{{member.Lon, member.Lat}}
			}
		case osmapi.TypeRelation, osmapi.TypeChangeset, osmapi.TypeNote, osmapi.TypeUser, osmapi.TypeBounds:
			// Relations as members are complex, skip for now in simple PBF path
		}
	}
	return nil
}

func nanodegree(f float64) int64 {
	return int64(f * 1_000_000_000)
}

func parseRadiusToInt(radiusStr string) int {
	radiusStr = strings.ToLower(radiusStr)
	var val float64
	var unit string

	switch {
	case strings.HasSuffix(radiusStr, "km"):
		unit = "km"
		_, _ = fmt.Sscanf(radiusStr, "%fkm", &val)
	case strings.HasSuffix(radiusStr, "m"):
		unit = "m"
		_, _ = fmt.Sscanf(radiusStr, "%fm", &val)
	default:
		_, _ = fmt.Sscanf(radiusStr, "%f", &val)
	}

	if unit == "km" {
		return int(val * 1000)
	}
	return int(val)
}

// computeDistanceMeters computes the Haversine distance between two points in meters.
func computeDistanceMeters(lat1, lon1, lat2, lon2 float64) int {
	const radius = 6371000 // Earth radius in meters
	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	deltaPhi := (lat2 - lat1) * math.Pi / 180
	deltaLambda := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(deltaPhi/2)*math.Sin(deltaPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*
			math.Sin(deltaLambda/2)*math.Sin(deltaLambda/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return int(radius * c)
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

// processPBFEntity processes an OSM entity and creates a search result match.
//
//nolint:revive // Processing entities requires handling many cases
func processPBFEntity(
	entityType string,
	id int64,
	tags map[string]string,
	coords [][]float64,
	queryLower string,
	params search.SearchParams,
	conf *config.Config,
	ont *PlaceTypeOntology,
	geosCtx *geos.Context,
) *bleveSearch.DocumentMatch {
	classifications := ClassifyMulti(tags, &conf.Importance, ont)
	if len(classifications) == 0 {
		return nil
	}

	NormalizeNameTag(tags, conf.Languages)
	EnhanceName(tags)

	if !matchFilters(classifications, tags, params, queryLower) {
		return nil
	}

	// All filters passed, create hit
	importance := classifications[0].Importance
	if tags["importance"] != "" {
		if val, err := strconv.ParseFloat(tags["importance"], 64); err == nil {
			// In some schemas, rank is 1-10 (lower is better).
			// If it's a small integer, we treat it as an OMT rank and invert it.
			if val > 0 && val <= 20 {
				importance = 20 - val
			} else {
				importance = val
			}
		}
	}

	score := computeDirectScore(tags, classifications, params, queryLower, importance)

	hit := &bleveSearch.DocumentMatch{
		ID:    fmt.Sprintf("%s/%d", entityType, id),
		Score: score,
		Fields: map[string]any{
			"name":          tags["name"],
			"key":           classifications[0].Key,
			"value":         classifications[0].Value,
			"importance":    importance,
			"phone":         tags["phone"],
			"wheelchair":    tags["wheelchair"],
			"opening_hours": tags["opening_hours"],
		},
	}
	if tags["phone"] == "" && tags["contact:phone"] != "" {
		hit.Fields["phone"] = tags["contact:phone"]
	}

	// Store other names
	for k, v := range tags {
		if strings.HasPrefix(k, "name:") || k == "alt_name" || k == "old_name" || k == "short_name" {
			hit.Fields[k] = v
		}
	}

	// Store address fields
	for _, k := range []string{
		"addr:housenumber", "addr:street", "addr:city", "addr:postcode",
		"addr:country", "addr:state", "addr:district", "addr:suburb",
		"addr:neighbourhood", "addr:floor", "addr:unit", "level",
	} {
		if v, ok := tags[k]; ok {
			hit.Fields[k] = v
		}
	}
	hit.Fields["display_address"] = computeDisplayAddress(tags)

	// Build geometry if requested
	if conf.GeometryMode != "no-geo" {
		geom, err := createGeometryFromCoords(coords, GeometryMode(conf.GeometryMode), conf.SimplificationTol, geosCtx)
		if err == nil {
			hit.Fields["geometry"] = geom
		}
	}

	return hit
}

func computeDirectScore(
	tags map[string]string,
	classifications []*Classification,
	params search.SearchParams,
	queryLower string,
	importance float64,
) float64 {
	if params.Query == "" {
		return importance
	}

	score := bestNameMatchScore(tags, params, queryLower)
	classificationScore := 0.0

	for _, c := range classifications {
		switch {
		case directTokenMatch(c.Value, queryLower):
			switch c.Key {
			case "cuisine":
				classificationScore = max(classificationScore, 320)
			case "amenity", "shop", "tourism", "leisure":
				classificationScore = max(classificationScore, 260)
			default:
				classificationScore = max(classificationScore, 180)
			}
		case directTokenMatch(c.Key, queryLower):
			classificationScore = max(classificationScore, 120)
		}
	}
	score += classificationScore

	if directTagValueMatch(tags["cuisine"], queryLower) {
		score += 340
		if hasFoodServiceClassification(classifications) {
			score += 40
		}
	}

	for _, match := range searchCategoryMatches(params.Query) {
		if hasClassification(classifications, match.Key, match.Value) {
			score += 280
			break
		}
	}

	if strings.TrimSpace(tags["name"]) == "" {
		score -= 300
	}

	if score == 0 {
		for _, v := range tags {
			if directTagValueMatch(v, queryLower) {
				score = 80
				break
			}
			if strings.Contains(strings.ToLower(v), queryLower) {
				score = 40
			}
		}
	}

	return score*1000 + importance
}

func bestNameMatchScore(tags map[string]string, params search.SearchParams, queryLower string) float64 {
	fields := []string{"name", "alt_name", "old_name", "short_name", "brand", "operator"}
	for _, lang := range params.Langs {
		fields = append(fields, "name:"+lang)
	}

	best := 0.0
	for _, field := range fields {
		value := strings.ToLower(tags[field])
		if value == "" {
			continue
		}
		switch {
		case directTokenMatch(value, queryLower):
			best = max(best, 360)
		case strings.Contains(value, queryLower):
			best = max(best, 400)
		}
	}
	return best
}

func directTagValueMatch(value, query string) bool {
	if value == "" {
		return false
	}
	if directTokenMatch(value, query) {
		return true
	}
	for _, part := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return r == ';' || r == ',' || r == '/'
	}) {
		if strings.TrimSpace(part) == query {
			return true
		}
	}
	return false
}

func directTokenMatch(value, query string) bool {
	return strings.TrimSpace(strings.ToLower(value)) == query
}

func hasFoodServiceClassification(classifications []*Classification) bool {
	for _, c := range classifications {
		if c.Key != "amenity" {
			continue
		}
		switch c.Value {
		case "restaurant", "fast_food", "cafe", "bar", "pub":
			return true
		}
	}
	return false
}

func searchCategoryMatches(q string) []search.CategoryMatch {
	if search.CategoryMapper == nil {
		return nil
	}
	normalized := strings.ToLower(strings.TrimSpace(q))
	if normalized == "" {
		return nil
	}
	matches := search.CategoryMapper(normalized)
	if len(matches) == 0 && strings.HasSuffix(normalized, "s") {
		matches = search.CategoryMapper(normalized[:len(normalized)-1])
	}
	return matches
}

func hasClassification(classifications []*Classification, key, value string) bool {
	for _, c := range classifications {
		if c.Key == key && c.Value == value {
			return true
		}
	}
	return false
}

func sortAndTruncateDirectHits(res *bleve.SearchResult, from, limit int) *bleve.SearchResult {
	if res == nil || len(res.Hits) == 0 {
		return res
	}

	bestByID := make(map[string]*bleveSearch.DocumentMatch, len(res.Hits))
	for _, hit := range res.Hits {
		existing, ok := bestByID[hit.ID]
		if !ok || hit.Score > existing.Score {
			bestByID[hit.ID] = hit
		}
	}

	deduped := make(bleveSearch.DocumentMatchCollection, 0, len(bestByID))
	for _, hit := range bestByID {
		deduped = append(deduped, hit)
	}

	sort.Slice(deduped, func(i, j int) bool {
		if deduped[i].Score == deduped[j].Score {
			return deduped[i].ID < deduped[j].ID
		}
		return deduped[i].Score > deduped[j].Score
	})

	res.Total = uint64(len(deduped))
	if from > 0 {
		if from >= len(deduped) {
			res.Hits = deduped[:0]
			return res
		}
		deduped = deduped[from:]
	}
	if limit > 0 && len(deduped) > limit {
		deduped = deduped[:limit]
	}
	res.Hits = deduped
	return res
}

func matchFilters(
	classifications []*Classification,
	tags map[string]string,
	params search.SearchParams,
	queryLower string,
) bool {
	if !matchKeyValue(classifications, params) {
		return false
	}
	if !MatchTextQuery(tags, params, queryLower) {
		return false
	}
	if !matchAddress(tags, params) {
		return false
	}
	if !MatchMetadata(tags, params) {
		return false
	}
	return true
}

func matchKeyValue(classifications []*Classification, params search.SearchParams) bool {
	keyMatched := params.Key == ""
	valueMatched := params.Value == ""

	for _, c := range classifications {
		if !keyMatched && c.Key == params.Key {
			keyMatched = true
		}
		if !valueMatched && c.Value == params.Value {
			valueMatched = true
		}
	}
	if !keyMatched || !valueMatched {
		return false
	}

	if len(params.Keys) > 0 && !matchMultiFilter(classifications, params.Keys, true) {
		return false
	}
	if len(params.Values) > 0 && !matchMultiFilter(classifications, params.Values, false) {
		return false
	}
	return true
}

func matchMultiFilter(classifications []*Classification, filters []string, isKey bool) bool {
	for _, f := range filters {
		for _, c := range classifications {
			val := c.Value
			if isKey {
				val = c.Key
			}
			if val == f {
				return true
			}
		}
	}
	return false
}

func MatchTextQuery(tags map[string]string, params search.SearchParams, queryLower string) bool {
	if params.Query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(tags["name"]), queryLower) {
		return true
	}
	for _, alt := range []string{"alt_name", "old_name", "short_name", "brand", "operator", "religion", "denomination"} {
		if strings.Contains(strings.ToLower(tags[alt]), queryLower) {
			return true
		}
	}
	for _, lang := range params.Langs {
		if strings.Contains(strings.ToLower(tags["name:"+lang]), queryLower) {
			return true
		}
	}

	// Optional: Search all tags if requested or as fallback
	for _, v := range tags {
		if strings.Contains(strings.ToLower(v), queryLower) {
			return true
		}
	}
	return false
}

func matchAddress(tags map[string]string, params search.SearchParams) bool {
	if params.Street != "" && !strings.Contains(strings.ToLower(tags["addr:street"]), strings.ToLower(params.Street)) {
		return false
	}
	if params.HouseNumber != "" && tags["addr:housenumber"] != params.HouseNumber {
		return false
	}
	if params.Postcode != "" && tags["addr:postcode"] != params.Postcode {
		return false
	}
	if params.City != "" {
		city := tags["addr:city"]
		if city == "" && (tags["place"] != "" || tags["class"] == "city" || tags["class"] == "town" || tags["class"] == "village") {
			city = tags["name"]
		}
		if !strings.Contains(strings.ToLower(city), strings.ToLower(params.City)) {
			return false
		}
	}
	if params.Country != "" &&
		!strings.Contains(strings.ToLower(tags["addr:country"]), strings.ToLower(params.Country)) {

		return false
	}
	if params.Floor != "" && tags["addr:floor"] != params.Floor {
		return false
	}
	if params.Unit != "" && tags["addr:unit"] != params.Unit {
		return false
	}
	if params.Level != "" && tags["level"] != params.Level {
		return false
	}
	return true
}

func MatchMetadata(tags map[string]string, params search.SearchParams) bool {
	if params.Phone != "" && !strings.Contains(tags["phone"], params.Phone) &&
		!strings.Contains(tags["contact:phone"], params.Phone) {

		return false
	}
	if params.Wheelchair != "" && tags["wheelchair"] != params.Wheelchair {
		return false
	}
	if params.OpeningHours != "" &&
		!strings.Contains(strings.ToLower(tags["opening_hours"]), strings.ToLower(params.OpeningHours)) {

		return false
	}
	return true
}

func computeDisplayAddress(tags map[string]string) string {
	var parts []string
	if hn := tags["addr:housenumber"]; hn != "" {
		parts = append(parts, hn)
	}
	if st := tags["addr:street"]; st != "" {
		parts = append(parts, st)
	}
	if city := tags["addr:city"]; city != "" {
		parts = append(parts, city)
	}
	if pc := tags["addr:postcode"]; pc != "" {
		parts = append(parts, pc)
	}
	return strings.Join(parts, ", ")
}

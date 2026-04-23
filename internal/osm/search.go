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
	"unicode"

	"github.com/blevesearch/bleve/v2"
	bleveSearch "github.com/blevesearch/bleve/v2/search"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
	"github.com/paulmach/orb"
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

func PBFSearch(pbfPath string, params search.SearchParams, conf *config.Config) (*bleve.SearchResult, error) {
	if search.IsNearQuery(params.Query) {
		return directNearSearch(params, func(nearParams search.SearchParams) (*bleve.SearchResult, error) {
			return pbfSearchRaw(pbfPath, nearParams, conf)
		})
	}

	return pbfSearchRaw(pbfPath, params, conf)
}

func pbfSearchRaw(pbfPath string, params search.SearchParams, conf *config.Config) (*bleve.SearchResult, error) {
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

			if !matchesCoordsSpatialFilter(coords, &filter, params, filter.radiusMeters) {
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

			if !matchesCoordsSpatialFilter(coords, &filter, params, filter.radiusMeters) {
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

func directNearSearch(
	baseParams search.SearchParams,
	searchFn func(search.SearchParams) (*bleve.SearchResult, error),
) (*bleve.SearchResult, error) {
	category, referencePlace, isNear := search.ParseNearQuery(baseParams.Query)
	if !isNear {
		return searchFn(baseParams)
	}

	refParams := search.SearchParams{
		Query:      referencePlace,
		Limit:      1,
		Langs:      baseParams.Langs,
		GeoMode:    baseParams.GeoMode,
		Fuzzy:      baseParams.Fuzzy,
		Prefix:     baseParams.Prefix,
		Analyzer:   baseParams.Analyzer,
		ExactMatch: baseParams.ExactMatch,
	}

	refResults, err := searchFn(refParams)
	if err != nil {
		return nil, err
	}

	if refResults == nil || len(refResults.Hits) == 0 {
		return &bleve.SearchResult{}, nil
	}

	lat, lon, ok := search.HitLatLon(refResults.Hits[0])
	if !ok {
		return &bleve.SearchResult{}, nil
	}

	searchParams := baseParams
	searchParams.Query = category
	searchParams.Lat = &lat
	searchParams.Lon = &lon
	searchParams.MinLat = nil
	searchParams.MaxLat = nil
	searchParams.MinLon = nil
	searchParams.MaxLon = nil
	if searchParams.Radius == "" {
		searchParams.Radius = "5000m"
	}

	return searchFn(searchParams)
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

func matchesCoordsSpatialFilter(
	coords [][]float64,
	filter *spatialFilter,
	params search.SearchParams,
	radiusMeters int,
) bool {
	if !filter.hasRadius && !filter.hasBbox {
		return true
	}

	for _, coord := range coords {
		if matchesCoordSpatialFilter(coord, filter, params, radiusMeters) {
			return true
		}
	}

	geom := coordsToOrbGeometry(coords)
	if geom == nil || !geometryBoundMatchesFilter(geom.Bound(), filter) {
		return false
	}

	if filter.hasRadius {
		center := orb.Point{*params.Lon, *params.Lat}
		return localMetricDistanceFrom(geom, center) <= float64(radiusMeters)
	}

	return true
}

func coordsToOrbGeometry(coords [][]float64) orb.Geometry {
	switch {
	case len(coords) == 0:
		return nil
	case len(coords) == 1:
		return orb.Point{coords[0][0], coords[0][1]}
	case len(coords) >= 4 &&
		coords[0][0] == coords[len(coords)-1][0] &&
		coords[0][1] == coords[len(coords)-1][1]:
		ring := make(orb.Ring, 0, len(coords))
		for _, coord := range coords {
			ring = append(ring, orb.Point{coord[0], coord[1]})
		}
		return orb.Polygon{ring}
	default:
		line := make(orb.LineString, 0, len(coords))
		for _, coord := range coords {
			line = append(line, orb.Point{coord[0], coord[1]})
		}
		return line
	}
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
		return search.SharedRankingScore(params, search.RankingSignals{
			Importance: importance,
		})
	}

	nameValues := search.CollectTagNameValues(tags, params.Langs)
	categoryScore := 0.0
	entityTier := search.EntityTierSecondary
	for _, c := range classifications {
		categoryScore = max(categoryScore, search.CategoryMatchScore(params.Query, c.Key, c.Value))
		entityTier = max(entityTier, search.EntityTierForClassification(c.Key, c.Value))
	}

	baseScore := 0.0
	for _, value := range tags {
		baseScore = max(baseScore, search.TextMatchScore(value, queryLower))
	}

	return search.SharedRankingScore(params, search.RankingSignals{
		BaseScore:          baseScore,
		NameMatchScore:     search.BestTextMatchScore(nameValues, params.Query),
		CategoryMatchScore: categoryScore,
		EntityTier:         entityTier,
		Importance:         importance,
		ExactNameMatch:     search.ExactNormalizedMatch(nameValues, params.Query),
		HasName:            len(nameValues) > 0,
	})
}

func directTextMatchScore(value, query string, params search.SearchParams) float64 {
	if value == "" || query == "" {
		return 0
	}

	valueLower := strings.ToLower(value)
	if strings.Contains(valueLower, query) {
		if directTokenMatch(value, query) {
			return 360
		}
		return 400
	}

	if params.Prefix && directPrefixMatch(value, query) {
		return 320
	}

	if params.Fuzzy && directFuzzyMatch(value, query) {
		return 240
	}

	return 0
}

func directPrefixMatch(value, query string) bool {
	compactQuery := compactDirectText(query)
	if compactQuery == "" {
		return false
	}

	if strings.HasPrefix(compactDirectText(value), compactQuery) {
		return true
	}

	for _, token := range tokenizeDirectText(value) {
		if strings.HasPrefix(token, compactQuery) {
			return true
		}
	}

	return false
}

func directFuzzyMatch(value, query string) bool {
	compactQuery := compactDirectText(query)
	if compactQuery == "" {
		return false
	}

	if boundedEditDistance(compactDirectText(value), compactQuery, 1) <= 1 {
		return true
	}

	for _, token := range tokenizeDirectText(value) {
		if boundedEditDistance(token, compactQuery, 1) <= 1 {
			return true
		}
	}

	return false
}

func compactDirectText(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func tokenizeDirectText(value string) []string {
	return strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func boundedEditDistance(a, b string, maxDistance int) int {
	if intAbs(len(a)-len(b)) > maxDistance {
		return maxDistance + 1
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		minInRow := curr[0]
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}

			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
			if curr[j] < minInRow {
				minInRow = curr[j]
			}
		}

		if minInRow > maxDistance {
			return maxDistance + 1
		}

		prev, curr = curr, prev
	}

	if prev[len(b)] > maxDistance {
		return maxDistance + 1
	}

	return prev[len(b)]
}

func intAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func directTokenMatch(value, query string) bool {
	return strings.TrimSpace(strings.ToLower(value)) == query
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
	if directTextMatchScore(tags["name"], queryLower, params) > 0 {
		return true
	}
	for _, alt := range []string{"alt_name", "old_name", "short_name", "brand", "operator", "religion", "denomination"} {
		if directTextMatchScore(tags[alt], queryLower, params) > 0 {
			return true
		}
	}
	for _, lang := range params.Langs {
		if directTextMatchScore(tags["name:"+lang], queryLower, params) > 0 {
			return true
		}
	}

	// Optional: Search all tags if requested or as fallback
	for _, v := range tags {
		if directTextMatchScore(v, queryLower, params) > 0 {
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
		if city == "" &&
			(tags["place"] != "" || tags["class"] == "city" || tags["class"] == "town" || tags["class"] == "village") {

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

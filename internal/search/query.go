package search

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/blevesearch/bleve/v2"
	blevesearch "github.com/blevesearch/bleve/v2/search"
	"github.com/blevesearch/bleve/v2/search/query"
)

type SearchParams struct {
	Query  string
	Lat    *float64
	Lon    *float64
	Radius string // e.g. "1000m"

	// Bounding Box
	MinLat *float64
	MaxLat *float64
	MinLon *float64
	MaxLon *float64

	Limit   int
	From    int // Offset for pagination
	Langs   []string
	GeoMode string

	// Advanced features
	Fuzzy    bool
	Prefix   bool
	Key      string
	Value    string
	PopBoost float64

	// Multi-value key/value filters (OR within each)
	Keys   []string
	Values []string

	// Analyzer type used during indexing (affects query strategy)
	Analyzer string

	// ExactMatch forces precise intersection checks (e.g. for PMTiles)
	ExactMatch bool

	// Address search fields
	Street      string
	HouseNumber string
	Postcode    string
	City        string
	Country     string
	Floor       string
	Unit        string
	Level       string

	// Metadata filters
	Phone        string
	Wheelchair   string
	OpeningHours string

	// Arbitrary tag filters, e.g. amenity=restaurant.
	Tags map[string]string
}

// QueryFields returns the number of query fields (words) in the search query.
// Used for word break penalty calculation.
func (p SearchParams) QueryFields() int {
	if p.Query == "" {
		return 0
	}
	// Count space-separated words
	count := 1
	for i := range len(p.Query) {
		if p.Query[i] == ' ' {
			count++
		}
	}
	return count
}

// MatchTier represents the quality of a name match.
type MatchTier int

const (
	TierExact  MatchTier = iota // Exact match (highest quality)
	TierPrefix                  // Prefix match
	TerFuzzy                    // Fuzzy/partial match (lowest quality)
)

// MatchTierBoost returns a score multiplier for the given match tier.
// Higher tier = higher boost to relevance score.
func MatchTierBoost(tier MatchTier) float64 {
	switch tier {
	case TierExact:
		return 3.0
	case TierPrefix:
		return 2.0
	case TerFuzzy:
		return 1.0
	default:
		return 1.0
	}
}

func addNameQuery(q, field string, fuzzy, prefix bool) query.Query {
	// Standard exact word match (high boost)
	mq := bleve.NewMatchQuery(q)
	mq.SetField(field)
	mq.Analyzer = "standard"
	mq.SetBoost(5.0)

	clauses := []query.Query{mq}

	if prefix {
		// Autocomplete edge_ngram match (normal boost)
		eq := bleve.NewMatchQuery(q)
		eq.SetField(field + "_edge_ngram")
		// Must use a standard analyzer for the query, otherwise the search term itself
		// is split into edge n-grams and matches any word starting with the first letter!
		eq.Analyzer = "standard"
		eq.SetBoost(1.0)
		clauses = append(clauses, eq)
	}

	if fuzzy {
		// Fuzzy match for typo tolerance (low boost)
		fq := bleve.NewMatchQuery(q)
		fq.SetField(field)
		fq.Analyzer = "standard"
		fq.SetFuzziness(1)
		fq.SetBoost(0.5)
		clauses = append(clauses, fq)
	}

	if len(clauses) == 1 {
		return mq
	}
	return bleve.NewDisjunctionQuery(clauses...)
}

func addKeywordQuery(q, field string, boost float64) query.Query {
	mq := bleve.NewMatchQuery(q)
	mq.SetField(field)
	mq.Analyzer = "keyword"
	mq.SetBoost(boost)
	return mq
}

func resolveCategoryMatches(q string) []CategoryMatch {
	if CategoryMapper == nil {
		return nil
	}

	normalized := strings.ToLower(strings.TrimSpace(q))
	if normalized == "" {
		return nil
	}

	matches := CategoryMapper(normalized)
	if len(matches) == 0 && strings.HasSuffix(normalized, "s") {
		matches = CategoryMapper(normalized[:len(normalized)-1])
	}
	return matches
}

func buildTextIntentQuery(params SearchParams) query.Query {
	normalized := normalizeQuery(params.Query)
	clauses := []query.Query{
		addNameQuery(normalized, "name", params.Fuzzy, params.Prefix),
		addNameQuery(normalized, "_search_names", params.Fuzzy, params.Prefix),
	}

	categoryMatches := resolveCategoryMatches(params.Query)

	if len(strings.Fields(normalized)) == 1 && !IsPlaceIntentQuery(params) {
		clauses = append(clauses,
			addKeywordQuery(normalized, "value", 7.0),
			addKeywordQuery(normalized, "values", 6.0),
			addKeywordQuery(normalized, "key", 2.0),
			addKeywordQuery(normalized, "keys", 1.5),
		)
	}

	for _, match := range categoryMatches {
		clauses = append(clauses,
			addKeywordQuery(match.Value, "value", 8.0),
			addKeywordQuery(match.Value, "values", 7.0),
		)
	}

	return bleve.NewDisjunctionQuery(clauses...)
}

// Search performs a search on the Bleve index with the given parameters.
// If the query matches a "X near Y" pattern, it uses NearSearch automatically.
// For multi-word queries, it may try multiple interpretations for better recall.
func Search(index bleve.Index, params SearchParams) (*bleve.SearchResult, error) {
	// Check for "X near Y" pattern and handle it via NearSearch
	if params.Query != "" && doIsNearQuery(params.Query) {
		category, referencePlace, isNear := doParseNearQuery(params.Query)
		if isNear {
			nearResult, err := NearSearch(index, params, category, referencePlace)
			if err != nil {
				return nil, err
			}
			return nearResult.Results, nil
		}
	}

	// Check if we should use multi-interpretation for better recall
	if params.Query != "" && shouldUseMultiInterpretation(params) {
		return executeInterpretations(index, params)
	}

	var q query.Query

	if params.Query != "" {
		q = buildTextIntentQuery(params)
	} else {
		q = bleve.NewMatchAllQuery()
	}

	if filterQuery := buildFilterQuery(params); filterQuery != nil {
		q = bleve.NewConjunctionQuery(q, filterQuery)
	}

	limit, from, fetchSize := effectiveLimitAndWindow(params)
	searchRequest := bleve.NewSearchRequest(q)
	// Fetch enough candidates for re-ranking. Offset is applied after re-ranking so
	// pages remain consistent with the final ordering.
	searchRequest.Size = fetchSize
	searchRequest.From = 0

	// Sort purely by score. Re-ranking will handle importance.
	searchRequest.SortBy([]string{"_score"})

	// Fields to return
	fields := resultFields(params)
	searchRequest.Fields = fields

	var exactRes *bleve.SearchResult
	if IsPlaceIntentQuery(params) {
		var exactErr error
		exactRes, exactErr = searchExactNameCandidates(index, params, fields)
		if exactErr != nil {
			return nil, exactErr
		}
	}

	res, err := index.Search(searchRequest)
	if err != nil {
		return nil, err
	}

	if exactRes != nil && len(exactRes.Hits) > 0 {
		mergeSearchHits(res, exactRes)
	}

	return ReRankAndTruncate(res, params, limit, from), nil
}

// buildFilterQuery combines all non-text filters into a single Bleve query.
// It is shared by the normal path and multi-interpretation path so filters are
// never silently dropped.
func buildFilterQuery(params SearchParams) query.Query {
	conjunctions := make([]query.Query, 0, 8)
	conjunctions = append(conjunctions, buildKeyValueFilters(params)...)
	if tagQuery := buildTagFilterQuery(params.Tags); tagQuery != nil {
		conjunctions = append(conjunctions, tagQuery)
	}
	if spatialQuery := buildSpatialQuery(params); spatialQuery != nil {
		conjunctions = append(conjunctions, spatialQuery)
	}
	conjunctions = append(conjunctions, buildAddressFilters(params)...)
	conjunctions = append(conjunctions, buildMetadataFilters(params)...)

	if len(conjunctions) == 0 {
		return nil
	}
	return bleve.NewConjunctionQuery(conjunctions...)
}

func buildKeyValueFilters(params SearchParams) []query.Query {
	conjunctions := make([]query.Query, 0, 2)

	keyList := append([]string(nil), params.Keys...)
	if params.Key != "" {
		keyList = append(keyList, params.Key)
	}
	if len(keyList) > 0 {
		keyQueries := make([]query.Query, 0, len(keyList)*2)
		for _, c := range keyList {
			cq1 := bleve.NewMatchQuery(c)
			cq1.SetField("key")
			cq2 := bleve.NewMatchQuery(c)
			cq2.SetField("keys")
			keyQueries = append(keyQueries, cq1, cq2)
		}
		conjunctions = append(conjunctions, bleve.NewDisjunctionQuery(keyQueries...))
	}

	valueList := append([]string(nil), params.Values...)
	if params.Value != "" {
		valueList = append(valueList, params.Value)
	}
	if len(valueList) > 0 {
		valueQueries := make([]query.Query, 0, len(valueList)*2)
		for _, s := range valueList {
			sq1 := bleve.NewMatchQuery(s)
			sq1.SetField("value")
			sq2 := bleve.NewMatchQuery(s)
			sq2.SetField("values")
			valueQueries = append(valueQueries, sq1, sq2)
		}
		conjunctions = append(conjunctions, bleve.NewDisjunctionQuery(valueQueries...))
	}

	return conjunctions
}

func buildSpatialQuery(params SearchParams) query.Query {
	if params.Lat != nil && params.Lon != nil && params.Radius != "" {
		if params.GeoMode == "geopoint" || params.GeoMode == "geopoint-centroid" {
			sq := bleve.NewGeoDistanceQuery(*params.Lon, *params.Lat, params.Radius)
			sq.SetField("geometry")
			return sq
		}
		if params.GeoMode != "" {
			// For geoshape, use a circle query with "intersects"
			sq, err := bleve.NewGeoShapeCircleQuery([]float64{*params.Lon, *params.Lat}, params.Radius, "intersects")
			if err == nil {
				sq.SetField("geometry")
				return sq
			}
		}
		return nil
	}

	if params.MinLat == nil || params.MaxLat == nil || params.MinLon == nil || params.MaxLon == nil {
		return nil
	}

	if params.GeoMode == "geopoint" || params.GeoMode == "geopoint-centroid" {
		// Top-left = [MinLon, MaxLat], Bottom-right = [MaxLon, MinLat]
		sq := bleve.NewGeoBoundingBoxQuery(*params.MinLon, *params.MaxLat, *params.MaxLon, *params.MinLat)
		sq.SetField("geometry")
		return sq
	}
	if params.GeoMode != "" {
		// For geoshape, use an envelope query (bbox)
		envelope := [][][][]float64{
			{
				{{*params.MinLon, *params.MaxLat}, {*params.MaxLon, *params.MinLat}},
			},
		}
		sq, err := bleve.NewGeoShapeQuery(envelope, "envelope", "intersects")
		if err == nil {
			sq.SetField("geometry")
			return sq
		}
	}
	return nil
}

func hasAddressFilters(params SearchParams) bool {
	return params.Street != "" ||
		params.HouseNumber != "" ||
		params.Postcode != "" ||
		params.City != "" ||
		params.Country != "" ||
		params.Floor != "" ||
		params.Unit != "" ||
		params.Level != ""
}

func buildAddressFilters(params SearchParams) []query.Query {
	if !hasAddressFilters(params) {
		return nil
	}

	conjunctions := make([]query.Query, 0, 8)
	addTextField := func(field, value, analyzer string) {
		if value == "" {
			return
		}
		sq := bleve.NewMatchQuery(value)
		sq.SetField(field)
		if analyzer != "" {
			sq.Analyzer = analyzer
		}
		conjunctions = append(conjunctions, sq)
	}
	addTextField("addr:street", params.Street, "")
	addTextField("addr:housenumber", params.HouseNumber, "keyword")
	addTextField("addr:postcode", params.Postcode, "keyword")
	addTextField("addr:city", params.City, "")
	addTextField("addr:country", params.Country, "")
	addTextField("addr:floor", params.Floor, "keyword")
	addTextField("addr:unit", params.Unit, "keyword")
	addTextField("level", params.Level, "keyword")
	return conjunctions
}

func buildMetadataFilters(params SearchParams) []query.Query {
	conjunctions := make([]query.Query, 0, 3)
	addTextField := func(field, value string) {
		if value == "" {
			return
		}
		sq := bleve.NewMatchQuery(value)
		sq.SetField(field)
		conjunctions = append(conjunctions, sq)
	}
	addTextField("phone", params.Phone)
	addTextField("wheelchair", params.Wheelchair)
	addTextField("opening_hours", params.OpeningHours)
	return conjunctions
}

// buildTagFilterQuery maps arbitrary tag filters onto the indexed primary
// classification fields. Most tagged POIs expose exactly one primary key/value
// pair (for example amenity=restaurant).
func buildTagFilterQuery(tags map[string]string) query.Query {
	if len(tags) == 0 {
		return nil
	}

	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	clauses := make([]query.Query, 0, len(keys))
	for _, key := range keys {
		value := tags[key]
		if key == "" || value == "" {
			continue
		}

		keyQuery := bleve.NewMatchQuery(key)
		keyQuery.SetField("key")
		keyQuery.Analyzer = "keyword"

		valueQuery := bleve.NewMatchQuery(value)
		valueQuery.SetField("value")
		valueQuery.Analyzer = "keyword"

		valuesQuery := bleve.NewMatchQuery(value)
		valuesQuery.SetField("values")
		valuesQuery.Analyzer = "keyword"

		clauses = append(clauses, bleve.NewConjunctionQuery(
			keyQuery,
			bleve.NewDisjunctionQuery(valueQuery, valuesQuery),
		))
	}

	if len(clauses) == 0 {
		return nil
	}
	return bleve.NewConjunctionQuery(clauses...)
}

// ParseRadiusMeters validates a radius string such as "1000m" or "5km".
func ParseRadiusMeters(radius string) (int, error) {
	value := strings.ToLower(strings.TrimSpace(radius))
	if value == "" {
		return 0, errors.New("radius is empty")
	}

	multiplier := 1.0
	switch {
	case strings.HasSuffix(value, "km"):
		multiplier = 1000
		value = strings.TrimSuffix(value, "km")
	case strings.HasSuffix(value, "m"):
		value = strings.TrimSuffix(value, "m")
	}

	meters, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid radius %q", radius)
	}
	if meters <= 0 {
		return 0, errors.New("radius must be greater than zero")
	}
	return int(meters * multiplier), nil
}

// DistanceMeters returns the Haversine distance between two coordinates.
func DistanceMeters(lat1, lon1, lat2, lon2 float64) int {
	const earthRadiusMeters = 6_371_000.0
	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	deltaPhi := (lat2 - lat1) * math.Pi / 180
	deltaLambda := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(deltaPhi/2)*math.Sin(deltaPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*math.Sin(deltaLambda/2)*math.Sin(deltaLambda/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return int(earthRadiusMeters * c)
}

func effectiveLimitAndWindow(params SearchParams) (limit, from, fetchSize int) {
	limit = params.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	from = max(params.From, 0)

	// Re-ranking changes the final order, so every page must be selected from
	// the same candidate window. Otherwise fetching a larger window for a later
	// page can introduce a new top result and make pages overlap.
	const rerankWindow = 10000
	fetchSize = rerankWindow
	return limit, from, fetchSize
}

func resultFields(params SearchParams) []string {
	fields := make([]string, 0, 29+4*len(params.Langs))
	fields = append(fields,
		"name", "alt_name", "old_name", "short_name", "brand", "operator",
		"key", "value", "keys", "values", "importance",
		"geometry", "distance_meters",
		"display_address",
		"phone", "wheelchair", "opening_hours",
	)
	// Add individual address fields for compatibility
	fields = append(fields,
		"addr:housenumber", "addr:street", "addr:city", "addr:postcode",
		"addr:country", "addr:state", "addr:district", "addr:suburb",
		"addr:neighbourhood", "addr:floor", "addr:unit", "level",
	)
	for _, lang := range params.Langs {
		fields = append(fields, "name:"+lang, "alt_name:"+lang, "old_name:"+lang, "short_name:"+lang)
	}
	return fields
}

// ReRankAndTruncate applies organic scoring combining the Bleve score with the POI's importance.
func ReRankAndTruncate(res *bleve.SearchResult, params SearchParams, limit, from int) *bleve.SearchResult {
	if res == nil || len(res.Hits) == 0 {
		return res
	}

	for _, hit := range res.Hits {
		nameValues := CollectNameValues(hit.Fields, params.Langs)
		key, value := primaryClassification(hit.Fields)
		hit.Score = SharedRankingScore(params, RankingSignals{
			BaseScore:          hit.Score,
			NameMatchScore:     BestTextMatchScore(nameValues, params.Query),
			CategoryMatchScore: categoryMatchScoreForHit(hit.Fields, params.Query),
			EntityTier:         hitEntityTier(hit.Fields, key, value),
			Importance:         hitImportance(hit.Fields),
			ExactNameMatch:     ExactNormalizedMatch(nameValues, params.Query),
			HasName:            len(nameValues) > 0,
		})
		addDistanceField(hit, params)
	}

	// Sort descending by new score. ID is a stable tie-breaker so pagination does
	// not shuffle equal-scoring rows between requests.
	sort.SliceStable(res.Hits, func(i, j int) bool {
		if res.Hits[i].Score == res.Hits[j].Score {
			return res.Hits[i].ID < res.Hits[j].ID
		}
		return res.Hits[i].Score > res.Hits[j].Score
	})

	if from >= len(res.Hits) {
		res.Hits = res.Hits[:0]
		return res
	}
	res.Hits = res.Hits[from:]

	// Truncate to limit
	if len(res.Hits) > limit {
		res.Hits = res.Hits[:limit]
	}

	res.MaxScore = 0
	for _, hit := range res.Hits {
		res.MaxScore = max(res.MaxScore, hit.Score)
	}

	return res
}

func addDistanceField(hit *blevesearch.DocumentMatch, params SearchParams) {
	if params.Lat == nil || params.Lon == nil {
		return
	}
	lat, lon, ok := HitLatLon(hit)
	if !ok {
		return
	}
	if hit.Fields == nil {
		hit.Fields = make(map[string]any)
	}
	hit.Fields["distance_meters"] = DistanceMeters(*params.Lat, *params.Lon, lat, lon)
}

func searchExactNameCandidates(index bleve.Index, params SearchParams, fields []string) (*bleve.SearchResult, error) {
	normalized := normalizeQuery(params.Query)
	exactQuery := bleve.NewMatchQuery(normalized)
	exactQuery.SetField("name")
	exactQuery.Analyzer = "standard"

	searchRequest := bleve.NewSearchRequest(exactQuery)
	searchRequest.Size = 2000
	searchRequest.Fields = fields
	searchRequest.SortBy([]string{"_score"})
	res, err := index.Search(searchRequest)
	if err != nil {
		return nil, err
	}

	filtered := &bleve.SearchResult{Hits: make(blevesearch.DocumentMatchCollection, 0, len(res.Hits))}
	for _, hit := range res.Hits {
		name, ok := hit.Fields["name"].(string)
		if !ok {
			continue
		}
		if normalizeQuery(strings.TrimSpace(name)) != normalized {
			continue
		}
		filtered.Hits = append(filtered.Hits, hit)
	}
	filtered.Total = uint64(len(filtered.Hits))
	return filtered, nil
}

func mergeSearchHits(base, extra *bleve.SearchResult) {
	if base == nil || extra == nil || len(extra.Hits) == 0 {
		return
	}

	merged := make(map[string]*blevesearch.DocumentMatch, len(base.Hits)+len(extra.Hits))
	for _, hit := range base.Hits {
		merged[hit.ID] = hit
	}
	for _, hit := range extra.Hits {
		existing, ok := merged[hit.ID]
		if !ok || hit.Score > existing.Score {
			merged[hit.ID] = hit
		}
	}

	base.Hits = base.Hits[:0]
	for _, hit := range merged {
		base.Hits = append(base.Hits, hit)
	}
	if base.Total < uint64(len(base.Hits)) {
		base.Total = uint64(len(base.Hits))
	}
}

func hitImportance(fields map[string]any) float64 {
	importance, _ := fields["importance"].(float64)
	return importance
}

func primaryClassification(fields map[string]any) (key, value string) {
	key, _ = fields["key"].(string)
	value, _ = fields["value"].(string)
	return key, value
}

func categoryMatchScoreForHit(fields map[string]any, query string) float64 {
	key, value := primaryClassification(fields)
	score := CategoryMatchScore(query, key, value)

	for _, pair := range classificationPairs(fields) {
		score = max(score, CategoryMatchScore(query, pair.Key, pair.Value))
	}

	return score
}

type classificationPair struct {
	Key   string
	Value string
}

// classificationPairs returns aligned key/value pairs from the multi-value
// fields. The keys and values arrays are parallel; treating every value as a
// value of the primary key incorrectly turns address components into category
// matches.
func classificationPairs(fields map[string]any) []classificationPair {
	keys := stringSliceField(fields["keys"])
	values := stringSliceField(fields["values"])
	count := min(len(keys), len(values))
	pairs := make([]classificationPair, 0, count)
	for i := range count {
		pairs = append(pairs, classificationPair{Key: keys[i], Value: values[i]})
	}
	return pairs
}

func stringSliceField(raw any) []string {
	switch values := raw.(type) {
	case []string:
		return values
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if s, ok := value.(string); ok {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}

func hitEntityTier(fields map[string]any, key, value string) EntityTier {
	tier := EntityTierForClassification(key, value)

	for _, pair := range classificationPairs(fields) {
		tier = max(tier, EntityTierForClassification(pair.Key, pair.Value))
	}

	return tier
}

package search

import (
	"sort"

	"github.com/blevesearch/bleve/v2"
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

func addNameQuery(q, field string) query.Query {
	// Standard exact word match (high boost)
	mq := bleve.NewMatchQuery(q)
	mq.SetField(field)
	mq.SetBoost(5.0)

	// Autocomplete edge_ngram match (normal boost)
	eq := bleve.NewMatchQuery(q)
	eq.SetField(field + "_edge_ngram")
	// Must use a standard analyzer for the query, otherwise the search term itself
	// is split into edge n-grams and matches any word starting with the first letter!
	eq.Analyzer = "standard"
	eq.SetBoost(1.0)

	// Fuzzy match for typo tolerance (low boost)
	fq := bleve.NewMatchQuery(q)
	fq.SetField(field)
	fq.SetFuzziness(1)
	fq.SetBoost(0.5)

	return bleve.NewDisjunctionQuery(mq, eq, fq)
}

// Search performs a search on the Bleve index with the given parameters.
// If the query matches a "X near Y" pattern, it uses NearSearch automatically.
// For multi-word queries, it may try multiple interpretations for better recall.
//
//nolint:revive,cyclop,funlen // Search requires handling many query type and spatial filtering cases
func Search(index bleve.Index, params SearchParams) (*bleve.SearchResult, error) {
	// Check for "X near Y" pattern and handle it via NearSearch
	if params.Query != "" && isNearQuery(params.Query) {
		category, referencePlace, isNear := parseNearQuery(params.Query)
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
		// Normalize the query for consistent matching
		normalized := normalizeQuery(params.Query)

		// Search across primary and combined search names
		nameQueries := []query.Query{
			addNameQuery(normalized, "name"),
			addNameQuery(normalized, "_search_names"),
		}
		q = bleve.NewDisjunctionQuery(nameQueries...)
	} else {
		q = bleve.NewMatchAllQuery()
	}

	// Filter by key and value (single value)
	keyFilter := params.Key
	valueFilter := params.Value

	// Filter by key and value (multi-value, OR within each)
	if len(params.Keys) > 0 {
		keyList := make([]string, 0, len(params.Keys)+1)
		keyList = append(keyList, params.Keys...)
		if keyFilter != "" {
			keyList = append(keyList, keyFilter)
		}
		keyQueries := make([]query.Query, 0, len(keyList)*2)
		for _, c := range keyList {
			cq1 := bleve.NewMatchQuery(c)
			cq1.SetField("key")
			cq2 := bleve.NewMatchQuery(c)
			cq2.SetField("keys")
			keyQueries = append(keyQueries, cq1, cq2)
		}
		q = bleve.NewConjunctionQuery(q, bleve.NewDisjunctionQuery(keyQueries...))
		keyFilter = "" // Already handled
	}

	if len(params.Values) > 0 {
		valueList := make([]string, 0, len(params.Values)+1)
		valueList = append(valueList, params.Values...)
		if valueFilter != "" {
			valueList = append(valueList, valueFilter)
		}
		valueQueries := make([]query.Query, 0, len(valueList)*2)
		for _, s := range valueList {
			sq1 := bleve.NewMatchQuery(s)
			sq1.SetField("value")
			sq2 := bleve.NewMatchQuery(s)
			sq2.SetField("values")
			valueQueries = append(valueQueries, sq1, sq2)
		}
		q = bleve.NewConjunctionQuery(q, bleve.NewDisjunctionQuery(valueQueries...))
		valueFilter = "" // Already handled
	}

	if keyFilter != "" || valueFilter != "" {
		conjunctions := []query.Query{q}
		if keyFilter != "" {
			// Search both primary and multi-key fields
			cq1 := bleve.NewMatchQuery(keyFilter)
			cq1.SetField("key")
			cq2 := bleve.NewMatchQuery(keyFilter)
			cq2.SetField("keys")
			conjunctions = append(conjunctions, bleve.NewDisjunctionQuery(cq1, cq2))
		}
		if valueFilter != "" {
			sq1 := bleve.NewMatchQuery(valueFilter)
			sq1.SetField("value")
			sq2 := bleve.NewMatchQuery(valueFilter)
			sq2.SetField("values")
			conjunctions = append(conjunctions, bleve.NewDisjunctionQuery(sq1, sq2))
		}
		q = bleve.NewConjunctionQuery(conjunctions...)
	}

	if params.Lat != nil && params.Lon != nil && params.Radius != "" {
		var spatialQuery query.Query
		if params.GeoMode == "geopoint" || params.GeoMode == "geopoint-centroid" {
			sq := bleve.NewGeoDistanceQuery(*params.Lon, *params.Lat, params.Radius)
			sq.SetField("geometry")
			spatialQuery = sq
		} else if params.GeoMode != "" {
			// For geoshape, use a circle query with "intersects"
			sq, err := bleve.NewGeoShapeCircleQuery([]float64{*params.Lon, *params.Lat}, params.Radius, "intersects")
			if err == nil {
				sq.SetField("geometry")
				spatialQuery = sq
			}
		}

		if spatialQuery != nil {
			q = bleve.NewConjunctionQuery(q, spatialQuery)
		}
	} else if params.MinLat != nil && params.MaxLat != nil && params.MinLon != nil && params.MaxLon != nil {
		var spatialQuery query.Query
		if params.GeoMode == "geopoint" || params.GeoMode == "geopoint-centroid" {
			// Top-left = [MinLon, MaxLat], Bottom-right = [MaxLon, MinLat]
			sq := bleve.NewGeoBoundingBoxQuery(*params.MinLon, *params.MaxLat, *params.MaxLon, *params.MinLat)
			sq.SetField("geometry")
			spatialQuery = sq
		} else if params.GeoMode != "" {
			// For geoshape, use an envelope query (bbox)
			envelope := [][][][]float64{
				{
					{{*params.MinLon, *params.MaxLat}, {*params.MaxLon, *params.MinLat}},
				},
			}
			sq, err := bleve.NewGeoShapeQuery(envelope, "envelope", "intersects")
			if err == nil {
				sq.SetField("geometry")
				spatialQuery = sq
			}
		}

		if spatialQuery != nil {
			q = bleve.NewConjunctionQuery(q, spatialQuery)
		}
	}

	// Address filters
	if params.Street != "" || params.HouseNumber != "" || params.Postcode != "" || params.City != "" ||
		params.Country != "" || params.Floor != "" || params.Unit != "" || params.Level != "" {

		conjunctions := []query.Query{q}
		if params.Street != "" {
			sq := bleve.NewMatchQuery(params.Street)
			sq.SetField("addr:street")
			conjunctions = append(conjunctions, sq)
		}
		if params.HouseNumber != "" {
			sq := bleve.NewMatchQuery(params.HouseNumber)
			sq.SetField("addr:housenumber")
			conjunctions = append(conjunctions, sq)
		}
		if params.Postcode != "" {
			sq := bleve.NewMatchQuery(params.Postcode)
			sq.SetField("addr:postcode")
			conjunctions = append(conjunctions, sq)
		}
		if params.City != "" {
			sq := bleve.NewMatchQuery(params.City)
			sq.SetField("addr:city")
			conjunctions = append(conjunctions, sq)
		}
		if params.Country != "" {
			sq := bleve.NewMatchQuery(params.Country)
			sq.SetField("addr:country")
			conjunctions = append(conjunctions, sq)
		}
		if params.Floor != "" {
			sq := bleve.NewMatchQuery(params.Floor)
			sq.SetField("addr:floor")
			conjunctions = append(conjunctions, sq)
		}
		if params.Unit != "" {
			sq := bleve.NewMatchQuery(params.Unit)
			sq.SetField("addr:unit")
			conjunctions = append(conjunctions, sq)
		}
		if params.Level != "" {
			sq := bleve.NewMatchQuery(params.Level)
			sq.SetField("level")
			conjunctions = append(conjunctions, sq)
		}
		q = bleve.NewConjunctionQuery(conjunctions...)
	}

	// Metadata filters
	if params.Phone != "" || params.Wheelchair != "" || params.OpeningHours != "" {
		conjunctions := []query.Query{q}
		if params.Phone != "" {
			sq := bleve.NewMatchQuery(params.Phone)
			sq.SetField("phone")
			conjunctions = append(conjunctions, sq)
		}
		if params.Wheelchair != "" {
			sq := bleve.NewMatchQuery(params.Wheelchair)
			sq.SetField("wheelchair")
			conjunctions = append(conjunctions, sq)
		}
		if params.OpeningHours != "" {
			sq := bleve.NewMatchQuery(params.OpeningHours)
			sq.SetField("opening_hours")
			conjunctions = append(conjunctions, sq)
		}
		q = bleve.NewConjunctionQuery(conjunctions...)
	}

	searchRequest := bleve.NewSearchRequest(q)
	originalLimit := params.Limit
	if originalLimit <= 0 {
		originalLimit = 100
	}
	if originalLimit > 1000 {
		originalLimit = 1000
	}
	// Fetch more results for re-ranking
	searchRequest.Size = min(originalLimit*3, 2000)

	searchRequest.From = params.From

	// Sort purely by score. Re-ranking will handle importance.
	searchRequest.SortBy([]string{"_score"})

	// Fields to return
	fields := []string{
		"name", "alt_name", "old_name", "short_name", "brand", "operator",
		"key", "value", "keys", "values", "importance",
		"geometry", "distance_meters",
		"display_address",
		"phone", "wheelchair", "opening_hours",
	}
	// Add individual address fields for compatibility
	fields = append(fields,
		"addr:housenumber", "addr:street", "addr:city", "addr:postcode",
		"addr:country", "addr:state", "addr:district", "addr:suburb",
		"addr:neighbourhood", "addr:floor", "addr:unit", "level",
	)
	for _, lang := range params.Langs {
		fields = append(fields, "name:"+lang, "alt_name:"+lang, "old_name:"+lang, "short_name:"+lang)
	}
	searchRequest.Fields = fields

	res, err := index.Search(searchRequest)
	if err != nil {
		return nil, err
	}

	return ReRankAndTruncate(res, originalLimit, params.PopBoost), nil
}

// ReRankAndTruncate applies organic scoring combining the Bleve score with the POI's importance.
func ReRankAndTruncate(res *bleve.SearchResult, limit int, popBoost float64) *bleve.SearchResult {
	if res == nil || len(res.Hits) == 0 {
		return res
	}

	if popBoost == 0 {
		popBoost = 0.2 // default fallback
	}

	for _, hit := range res.Hits {
		importance := 0.0
		if impVal, ok := hit.Fields["importance"].(float64); ok {
			importance = impVal
		}
		// FinalScore = BleveScore * (1.0 + ImportanceBoost)
		finalScore := hit.Score * (1.0 + (importance * popBoost))
		// We overwrite Score for sorting
		hit.Score = finalScore
	}

	// Sort descending by new score
	sort.Slice(res.Hits, func(i, j int) bool {
		return res.Hits[i].Score > res.Hits[j].Score
	})

	// Truncate to limit
	if len(res.Hits) > limit {
		res.Hits = res.Hits[:limit]
	}

	return res
}

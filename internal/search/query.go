package search

import (
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
	Fuzzy   bool
	Prefix  bool
	Class   string
	Subtype string

	// Multi-value class/subtype filters (OR within each)
	Classes  []string
	Subtypes []string

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

func addNameQuery(q string, fuzzy, prefix bool, field, analyzer string) query.Query {
	// For keyword analyzer: exact match only
	if analyzer == "keyword" {
		mq := bleve.NewMatchQuery(q)
		mq.SetField(field)
		mq.SetBoost(MatchTierBoost(TierExact))
		return mq
	}

	// For edge_ngram analyzer: indexed tokens are prefixes,
	// so a MatchQuery will match on prefix tokens automatically.
	// We don't need the Prefix flag - just use MatchQuery.
	if analyzer == "edge_ngram" {
		mq := bleve.NewMatchQuery(q)
		mq.SetField(field)
		mq.SetBoost(MatchTierBoost(TierExact))
		return mq
	}

	// For ngram analyzer: indexed tokens are substrings,
	// so a MatchQuery will match on substring tokens automatically.
	if analyzer == "ngram" {
		mq := bleve.NewMatchQuery(q)
		mq.SetField(field)
		mq.SetBoost(MatchTierBoost(TierExact))
		return mq
	}

	// Default: standard analyzer behavior
	if prefix {
		pq := bleve.NewPrefixQuery(q)
		pq.SetField(field)
		pq.SetBoost(MatchTierBoost(TierPrefix))
		return pq
	}
	mq := bleve.NewMatchQuery(q)
	mq.SetField(field)
	if fuzzy {
		mq.SetFuzziness(1)
		mq.SetBoost(MatchTierBoost(TerFuzzy))
	} else {
		// Non-fuzzy, non-prefix = exact match
		mq.SetBoost(MatchTierBoost(TierExact))
	}
	return mq
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

		analyzer := params.Analyzer
		if analyzer == "" {
			analyzer = "standard"
		}

		// Search across multiple name fields
		nameQueries := make([]query.Query, 0, 4+4*len(params.Langs))
		nameQueries = append(nameQueries,
			addNameQuery(normalized, params.Fuzzy, params.Prefix, "name", analyzer),
			addNameQuery(normalized, params.Fuzzy, params.Prefix, "alt_name", analyzer),
			addNameQuery(normalized, params.Fuzzy, params.Prefix, "old_name", analyzer),
			addNameQuery(normalized, params.Fuzzy, params.Prefix, "short_name", analyzer),
		)
		for _, lang := range params.Langs {
			nameQueries = append(
				nameQueries,
				addNameQuery(normalized, params.Fuzzy, params.Prefix, "name:"+lang, analyzer),
			)
			nameQueries = append(
				nameQueries,
				addNameQuery(normalized, params.Fuzzy, params.Prefix, "alt_name:"+lang, analyzer),
			)
			nameQueries = append(
				nameQueries,
				addNameQuery(normalized, params.Fuzzy, params.Prefix, "old_name:"+lang, analyzer),
			)
			nameQueries = append(
				nameQueries,
				addNameQuery(normalized, params.Fuzzy, params.Prefix, "short_name:"+lang, analyzer),
			)
		}
		q = bleve.NewDisjunctionQuery(nameQueries...)
	} else {
		q = bleve.NewMatchAllQuery()
	}

	// Filter by class and subtype (single value)
	classFilter := params.Class
	subtypeFilter := params.Subtype

	// Filter by class and subtype (multi-value, OR within each)
	if len(params.Classes) > 0 {
		classList := make([]string, 0, len(params.Classes)+1)
		classList = append(classList, params.Classes...)
		if classFilter != "" {
			classList = append(classList, classFilter)
		}
		classQueries := make([]query.Query, 0, len(classList)*2)
		for _, c := range classList {
			cq1 := bleve.NewMatchQuery(c)
			cq1.SetField("class")
			cq2 := bleve.NewMatchQuery(c)
			cq2.SetField("classes")
			classQueries = append(classQueries, cq1, cq2)
		}
		q = bleve.NewConjunctionQuery(q, bleve.NewDisjunctionQuery(classQueries...))
		classFilter = "" // Already handled
	}

	if len(params.Subtypes) > 0 {
		subtypeList := make([]string, 0, len(params.Subtypes)+1)
		subtypeList = append(subtypeList, params.Subtypes...)
		if subtypeFilter != "" {
			subtypeList = append(subtypeList, subtypeFilter)
		}
		subtypeQueries := make([]query.Query, 0, len(subtypeList)*2)
		for _, s := range subtypeList {
			sq1 := bleve.NewMatchQuery(s)
			sq1.SetField("subtype")
			sq2 := bleve.NewMatchQuery(s)
			sq2.SetField("subtypes")
			subtypeQueries = append(subtypeQueries, sq1, sq2)
		}
		q = bleve.NewConjunctionQuery(q, bleve.NewDisjunctionQuery(subtypeQueries...))
		subtypeFilter = "" // Already handled
	}

	if classFilter != "" || subtypeFilter != "" {
		conjunctions := []query.Query{q}
		if classFilter != "" {
			// Search both primary and multi-class fields
			cq1 := bleve.NewMatchQuery(classFilter)
			cq1.SetField("class")
			cq2 := bleve.NewMatchQuery(classFilter)
			cq2.SetField("classes")
			conjunctions = append(conjunctions, bleve.NewDisjunctionQuery(cq1, cq2))
		}
		if subtypeFilter != "" {
			sq1 := bleve.NewMatchQuery(subtypeFilter)
			sq1.SetField("subtype")
			sq2 := bleve.NewMatchQuery(subtypeFilter)
			sq2.SetField("subtypes")
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
	searchRequest.Size = params.Limit
	if searchRequest.Size == 0 {
		searchRequest.Size = 10
	}
	searchRequest.From = params.From

	searchRequest.SortBy([]string{"-importance", "_score"})

	// Fields to return
	fields := make([]string, 0, 10+4*len(params.Langs))
	fields = append(fields,
		"name",
		"alt_name",
		"old_name",
		"short_name",
		"class",
		"subtype",
		"classes",
		"subtypes",
		"importance",
		"geometry",
		"distance_meters",
	)
	// Add address fields when any address filter is used
	if params.Street != "" || params.HouseNumber != "" || params.Postcode != "" || params.City != "" ||
		params.Country != "" || params.Floor != "" || params.Unit != "" || params.Level != "" ||
		params.Phone != "" || params.Wheelchair != "" || params.OpeningHours != "" {

		fields = append(fields,
			"addr:housenumber", "addr:street", "addr:city", "addr:postcode",
			"addr:country", "addr:state", "addr:district", "addr:suburb",
			"addr:neighbourhood", "addr:floor", "addr:unit", "level",
			"phone", "wheelchair", "opening_hours",
		)
	}
	for _, lang := range params.Langs {
		fields = append(fields, "name:"+lang, "alt_name:"+lang, "old_name:"+lang, "short_name:"+lang)
	}
	searchRequest.Fields = fields

	return index.Search(searchRequest)
}

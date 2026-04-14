package search

import (
	"strings"

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
		tq := bleve.NewTermQuery(q)
		tq.SetField(field)
		tq.SetBoost(MatchTierBoost(TierExact))
		return tq
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

// normalizeQuery strips punctuation, collapses whitespace, and lowercases.
func normalizeQuery(q string) string {
	q = strings.TrimSpace(q)
	// Remove common punctuation that doesn't aid search
	var b strings.Builder
	for _, r := range q {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '-', r == '_':
			b.WriteRune(r)
		case r == '\'' || r == '"' || r == '.' || r == ',' || r == '(' || r == ')':
			// Skip these punctuation marks
			continue
		default:
			b.WriteRune(r)
		}
	}
	q = b.String()
	// Collapse multiple spaces
	var out strings.Builder
	space := false
	for _, r := range q {
		if r == ' ' {
			if !space {
				out.WriteRune(' ')
			}
			space = true
		} else {
			out.WriteRune(r)
			space = false
		}
	}
	return strings.ToLower(strings.TrimSpace(out.String()))
}

func Search(index bleve.Index, params SearchParams) (*bleve.SearchResult, error) {
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
			cq1 := bleve.NewTermQuery(c)
			cq1.SetField("class")
			cq2 := bleve.NewTermQuery(c)
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
			sq1 := bleve.NewTermQuery(s)
			sq1.SetField("subtype")
			sq2 := bleve.NewTermQuery(s)
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
			cq1 := bleve.NewTermQuery(classFilter)
			cq1.SetField("class")
			cq2 := bleve.NewTermQuery(classFilter)
			cq2.SetField("classes")
			conjunctions = append(conjunctions, bleve.NewDisjunctionQuery(cq1, cq2))
		}
		if subtypeFilter != "" {
			sq1 := bleve.NewTermQuery(subtypeFilter)
			sq1.SetField("subtype")
			sq2 := bleve.NewTermQuery(subtypeFilter)
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

	searchRequest := bleve.NewSearchRequest(q)
	searchRequest.Size = params.Limit
	if searchRequest.Size == 0 {
		searchRequest.Size = 10
	}

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
	)
	for _, lang := range params.Langs {
		fields = append(fields, "name:"+lang, "alt_name:"+lang, "old_name:"+lang, "short_name:"+lang)
	}
	searchRequest.Fields = fields

	return index.Search(searchRequest)
}

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
	Langs   []string
	GeoMode string

	// Advanced features
	Fuzzy   bool
	Prefix  bool
	Class   string
	Subtype string
}

func addNameQuery(q string, fuzzy bool, prefix bool, field string) query.Query {
	if prefix {
		pq := bleve.NewPrefixQuery(q)
		pq.SetField(field)
		return pq
	}
	mq := bleve.NewMatchQuery(q)
	mq.SetField(field)
	if fuzzy {
		mq.SetFuzziness(1)
	}
	return mq
}

func Search(index bleve.Index, params SearchParams) (*bleve.SearchResult, error) {
	var q query.Query

	if params.Query != "" {
		// Search across multiple name fields
		nameQueries := []query.Query{
			addNameQuery(params.Query, params.Fuzzy, params.Prefix, "name"),
			addNameQuery(params.Query, params.Fuzzy, params.Prefix, "alt_name"),
			addNameQuery(params.Query, params.Fuzzy, params.Prefix, "old_name"),
			addNameQuery(params.Query, params.Fuzzy, params.Prefix, "short_name"),
		}
		for _, lang := range params.Langs {
			nameQueries = append(nameQueries, addNameQuery(params.Query, params.Fuzzy, params.Prefix, "name:"+lang))
			nameQueries = append(nameQueries, addNameQuery(params.Query, params.Fuzzy, params.Prefix, "alt_name:"+lang))
			nameQueries = append(nameQueries, addNameQuery(params.Query, params.Fuzzy, params.Prefix, "old_name:"+lang))
			nameQueries = append(nameQueries, addNameQuery(params.Query, params.Fuzzy, params.Prefix, "short_name:"+lang))
		}
		q = bleve.NewDisjunctionQuery(nameQueries...)
	} else {
		q = bleve.NewMatchAllQuery()
	}

	// Filter by class and subtype
	if params.Class != "" || params.Subtype != "" {
		conjunctions := []query.Query{q}
		if params.Class != "" {
			cq := bleve.NewTermQuery(params.Class)
			cq.SetField("class")
			conjunctions = append(conjunctions, cq)
		}
		if params.Subtype != "" {
			sq := bleve.NewTermQuery(params.Subtype)
			sq.SetField("subtype")
			conjunctions = append(conjunctions, sq)
		}
		q = bleve.NewConjunctionQuery(conjunctions...)
	}

	if params.Lat != nil && params.Lon != nil && params.Radius != "" {
		var spatialQuery query.Query
		if params.GeoMode == "geopoint" {
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
		if params.GeoMode == "geopoint" {
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
	fields := []string{"id", "name", "alt_name", "old_name", "short_name", "class", "subtype", "importance", "geometry"}
	for _, lang := range params.Langs {
		fields = append(fields, "name:"+lang, "alt_name:"+lang, "old_name:"+lang, "short_name:"+lang)
	}
	searchRequest.Fields = fields

	return index.Search(searchRequest)
}

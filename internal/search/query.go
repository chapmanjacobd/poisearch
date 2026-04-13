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
}

func Search(index bleve.Index, params SearchParams) (*bleve.SearchResult, error) {
	var q query.Query

	if params.Query != "" {
		// Search across multiple name fields
		nameQueries := []query.Query{
			bleve.NewMatchQuery(params.Query),
		}
		for _, lang := range params.Langs {
			mq := bleve.NewMatchQuery(params.Query)
			mq.SetField("name:" + lang)
			nameQueries = append(nameQueries, mq)
		}
		q = bleve.NewDisjunctionQuery(nameQueries...)
	} else {
		q = bleve.NewMatchAllQuery()
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
			// bleve wants [top-left-lon, top-left-lat] and [bottom-right-lon, bottom-right-lat]
			// We use MinLat/MaxLat MinLon/MaxLon for clarity.
			// Top-left = [MinLon, MaxLat], Bottom-right = [MaxLon, MinLat]
			sq := bleve.NewGeoBoundingBoxQuery(*params.MinLon, *params.MaxLat, *params.MaxLon, *params.MinLat)
			sq.SetField("geometry")
			spatialQuery = sq
		} else if params.GeoMode != "" {
			// For geoshape, use an envelope query (bbox)
			// NewGeoShapeQuery expects [][][][]float64
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

	// Sorting: Importance first, then score.
	// If we have a location, we might want to sort by distance,
	// but Bleve's GeoDistanceQuery doesn't automatically add a "distance" field for sorting easily
	// without using a SortBy that includes a GeoDistanceSort.
	// For now, stick to Importance + Score.
	searchRequest.SortBy([]string{"-importance", "_score"})

	// Fields to return
	searchRequest.Fields = []string{"id", "name", "class", "subtype", "importance", "geometry"}
	for _, lang := range params.Langs {
		searchRequest.Fields = append(searchRequest.Fields, "name:"+lang)
	}

	return index.Search(searchRequest)
}

package osm

import (
	"context"
	"fmt"
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
		Hits:   make(bleveSearch.DocumentMatchCollection, 0),
		Status: &bleve.SearchStatus{Total: 1, Successful: 1},
	}

	geosCtx := geos.NewContext()
	queryLower := strings.ToLower(params.Query)

	for scanner.Scan() {
		obj := scanner.Object()
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
		classification := Classify(tags, &conf.Importance)
		if classification == nil {
			continue
		}
		if params.Class != "" && classification.Class != params.Class {
			continue
		}
		if params.Subtype != "" && classification.Subtype != params.Subtype {
			continue
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

		// 3. Spatial Filter
		var geom any
		if params.Lat != nil && params.Lon != nil && params.Radius != "" {
			g, err := CreateGeometry(obj, ModeGeopoint, 0, geosCtx)
			if err != nil {
				continue
			}
			geom = g
		}

		hit := &bleveSearch.DocumentMatch{
			ID:    fmt.Sprintf("%s/%d", obj.ObjectID().Type(), id),
			Score: classification.Importance,
			Fields: map[string]interface{}{
				"name":     tags["name"],
				"class":    classification.Class,
				"subtype":  classification.Subtype,
				"geometry": geom,
			},
		}
		res.Hits = append(res.Hits, hit)
		res.Total++

		if len(res.Hits) >= params.Limit && params.Limit > 0 {
			break
		}
	}

	return res, scanner.Err()
}

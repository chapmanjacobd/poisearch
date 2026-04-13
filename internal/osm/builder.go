package osm

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
	"github.com/twpayne/go-geos"
)

func BuildIndex(inputPath string, conf *config.Config, index bleve.Index) error {
	f, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := osmpbf.New(context.Background(), f, 4)
	defer scanner.Close()

	geosCtx := geos.NewContext()
	count := 0
	batch := index.NewBatch()
	batchSize := 1000

	for scanner.Scan() {
		obj := scanner.Object()
		var tags map[string]string
		var id int64

		switch o := obj.(type) {
		case *osm.Node:
			tags = o.TagMap()
			id = int64(o.ID)
		case *osm.Way:
			tags = o.TagMap()
			id = int64(o.ID)
		case *osm.Relation:
			tags = o.TagMap()
			id = int64(o.ID)
		default:
			continue
		}

		classification := Classify(tags, &conf.Importance)
		if classification == nil {
			continue
		}

		geom, err := CreateGeometry(obj, GeometryMode(conf.GeometryMode), conf.SimplificationTol, geosCtx)
		if err != nil {
			// Skip objects with invalid geometry
			continue
		}

		feature := &search.Feature{
			ID:         fmt.Sprintf("%s/%d", obj.ObjectID().Type(), id),
			Name:       tags["name"],
			Names:      make(map[string]string),
			Class:      classification.Class,
			Subtype:    classification.Subtype,
			Importance: classification.Importance,
			Geometry:   geom,
		}

		for _, lang := range conf.Languages {
			if name, ok := tags["name:"+lang]; ok {
				feature.Names["name:"+lang] = name
			}
		}

		err = batch.Index(feature.ID, search.FeatureToMap(feature))
		if err != nil {
			log.Printf("error indexing feature %s: %v", feature.ID, err)
			continue
		}

		count++
		if count%batchSize == 0 {
			err = index.Batch(batch)
			if err != nil {
				return err
			}
			batch = index.NewBatch()
			if count%10000 == 0 {
				log.Printf("indexed %d features...", count)
			}
		}
	}

	if batch.Size() > 0 {
		err = index.Batch(batch)
		if err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	log.Printf("Finished! Indexed %d features.", count)
	return nil
}

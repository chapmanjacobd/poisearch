package osm

import (
	"context"
	"fmt"
	"log/slog"
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

	// Load Wikidata importance lookup if configured
	var wdLookup *WikidataLookup
	if conf.WikidataImportance != "" {
		wdLookup, err = LoadWikidataImportance(conf.WikidataImportance)
		if err != nil {
			slog.Warn("failed to load wikidata importance file, continuing without it", "error", err)
		} else {
			slog.Info("loaded wikidata importance scores", "count", wdLookup.Size())
		}
	}

	// Load place type ontology if configured
	var ont *PlaceTypeOntology
	if conf.OntologyPath != "" {
		ont, err = LoadOntologyFromCSV(conf.OntologyPath)
		if err != nil {
			slog.Warn("failed to load ontology file, continuing without it", "error", err)
		} else {
			slog.Info("loaded place type ontology", "entries", len(ont.levels))
		}
	} else {
		// Use default built-in ontology
		ont = DefaultOntology()
	}

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

		if conf.OnlyNamed {
			hasName := tags["name"] != ""
			if !hasName {
				// Check alt names if not disabled
				if !conf.DisableAltNames {
					altNames := []string{"alt_name", "old_name", "short_name"}
					for _, alt := range altNames {
						if tags[alt] != "" {
							hasName = true
							break
						}
					}
				}
				// Check translations
				if !hasName {
					for _, lang := range conf.Languages {
						if tags["name:"+lang] != "" {
							hasName = true
							break
						}
					}
				}
			}
			if !hasName {
				continue
			}
		}

		classifications := ClassifyMulti(tags, &conf.Importance, ont)
		if len(classifications) == 0 {
			continue
		}

		var geom any
		var err error
		if ModeNoGeo != GeometryMode(conf.GeometryMode) {
			geom, err = CreateGeometry(obj, GeometryMode(conf.GeometryMode), conf.SimplificationTol, geosCtx)
			if err != nil {
				// Skip objects with invalid geometry
				continue
			}
		}

		// Use the highest-importance classification as primary
		best := classifications[0]
		for _, c := range classifications[1:] {
			if c.Importance > best.Importance {
				best = c
			}
		}

		// Apply Wikidata importance boost if available
		if wdLookup != nil && tags["wikidata"] != "" {
			wdImportance := wdLookup.GetImportance(tags["wikidata"])
			if wdImportance > 0 {
				// Boost importance significantly for Wikidata-matched POIs
				// Scale: wikidata importance (0-1) is added to the base importance
				best.Importance += wdImportance * 10.0
			}
		}

		feature := &search.Feature{
			ID:         fmt.Sprintf("%s/%d", obj.ObjectID().Type(), id),
			Name:       tags["name"],
			Names:      make(map[string]string),
			Importance: best.Importance,
			Geometry:   geom,
			Class:      best.Class,
			Subtype:    best.Subtype,
		}

		if !conf.DisableImportance {
			feature.Importance = best.Importance
		} else {
			feature.Importance = 0
		}

		if !conf.DisableClassSubtype {
			// Collect all classes and subtypes for multi-class support
			classes := make([]string, len(classifications))
			subtypes := make([]string, len(classifications))
			for i, c := range classifications {
				classes[i] = c.Class
				subtypes[i] = c.Subtype
			}
			feature.Classes = classes
			feature.Subtypes = subtypes
		}

		if !conf.DisableAltNames {
			altNames := []string{"alt_name", "old_name", "short_name"}
			for _, alt := range altNames {
				if val, ok := tags[alt]; ok {
					feature.Names[alt] = val
				}
			}

			for _, lang := range conf.Languages {
				if name, ok := tags["name:"+lang]; ok {
					feature.Names["name:"+lang] = name
				}
				for _, alt := range altNames {
					if val, ok := tags[alt+":"+lang]; ok {
						feature.Names[alt+":"+lang] = val
					}
				}
			}
		} else {
			// Still index translations of "name" if languages are configured
			for _, lang := range conf.Languages {
				if name, ok := tags["name:"+lang]; ok {
					feature.Names["name:"+lang] = name
				}
			}
		}

		err = batch.Index(feature.ID, search.FeatureToMap(feature))
		if err != nil {
			slog.Error("error indexing feature", "id", feature.ID, "error", err)
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
				slog.Info("indexed features", "count", count)
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

	slog.Info("Finished!", "indexed_features", count)
	return nil
}

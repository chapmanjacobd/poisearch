package search

import (
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/chapmanjacobd/poisearch/internal/config"
)

func BuildIndexMapping(conf *config.Config) mapping.IndexMapping {
	indexMapping := bleve.NewIndexMapping()

	docMapping := bleve.NewDocumentMapping()

	// Disable _all field to save space
	docMapping.Enabled = true
	docMapping.Dynamic = false // Only index defined fields

	// Name fields
	nameFieldMapping := bleve.NewTextFieldMapping()
	nameFieldMapping.Analyzer = "en"
	nameFieldMapping.IncludeInAll = false
	nameFieldMapping.IncludeTermVectors = false // Saves space, no highlighting needed
	nameFieldMapping.Store = true               // Always store names so results are useful
	docMapping.AddFieldMappingsAt("name", nameFieldMapping)
	docMapping.AddFieldMappingsAt("alt_name", nameFieldMapping)
	docMapping.AddFieldMappingsAt("old_name", nameFieldMapping)
	docMapping.AddFieldMappingsAt("short_name", nameFieldMapping)

	for _, lang := range conf.Languages {
		docMapping.AddFieldMappingsAt("name:"+lang, nameFieldMapping)
		docMapping.AddFieldMappingsAt("alt_name:"+lang, nameFieldMapping)
		docMapping.AddFieldMappingsAt("old_name:"+lang, nameFieldMapping)
		docMapping.AddFieldMappingsAt("short_name:"+lang, nameFieldMapping)
	}

	// Class and Subtype
	keywordMapping := bleve.NewTextFieldMapping()
	keywordMapping.Analyzer = "keyword"
	keywordMapping.IncludeInAll = false
	keywordMapping.IncludeTermVectors = false
	keywordMapping.Store = conf.StoreMetadata
	docMapping.AddFieldMappingsAt("class", keywordMapping)
	docMapping.AddFieldMappingsAt("subtype", keywordMapping)

	// Importance
	numMapping := bleve.NewNumericFieldMapping()
	numMapping.IncludeInAll = false
	numMapping.Store = conf.StoreMetadata
	docMapping.AddFieldMappingsAt("importance", numMapping)

	// Geometry
	geoMode := conf.GeometryMode
	if geoMode != "" && geoMode != "no-geo" {
		if geoMode == "geopoint" || geoMode == "geopoint-centroid" {
			geoMapping := bleve.NewGeoPointFieldMapping()
			geoMapping.Store = conf.StoreGeometry
			docMapping.AddFieldMappingsAt("geometry", geoMapping)
		} else {
			geoMapping := bleve.NewGeoShapeFieldMapping()
			geoMapping.Store = conf.StoreGeometry
			docMapping.AddFieldMappingsAt("geometry", geoMapping)
		}
	}

	indexMapping.DefaultMapping = docMapping
	return indexMapping
}

package search

import (
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
)

func BuildIndexMapping(langs []string, geoMode string) mapping.IndexMapping {
	indexMapping := bleve.NewIndexMapping()

	docMapping := bleve.NewDocumentMapping()

	// Name fields
	nameFieldMapping := bleve.NewTextFieldMapping()
	nameFieldMapping.Analyzer = "en" // Default analyzer
	docMapping.AddFieldMappingsAt("name", nameFieldMapping)

	for _, lang := range langs {
		docMapping.AddFieldMappingsAt("name:"+lang, nameFieldMapping)
	}

	// Class and Subtype
	keywordMapping := bleve.NewTextFieldMapping()
	keywordMapping.Analyzer = "keyword"
	docMapping.AddFieldMappingsAt("class", keywordMapping)
	docMapping.AddFieldMappingsAt("subtype", keywordMapping)

	// Importance
	numMapping := bleve.NewNumericFieldMapping()
	docMapping.AddFieldMappingsAt("importance", numMapping)

	// Geometry
	if geoMode != "" {
		if geoMode == "geopoint" {
			geoMapping := bleve.NewGeoPointFieldMapping()
			docMapping.AddFieldMappingsAt("geometry", geoMapping)
		} else {
			geoMapping := bleve.NewGeoShapeFieldMapping()
			docMapping.AddFieldMappingsAt("geometry", geoMapping)
		}
	}

	indexMapping.DefaultMapping = docMapping
	return indexMapping
}

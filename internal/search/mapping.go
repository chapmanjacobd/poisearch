package search

import (
	"fmt"

	"github.com/blevesearch/bleve/v2"
	_ "github.com/blevesearch/bleve/v2/analysis/analyzer/custom" // Register custom analyzer
	_ "github.com/blevesearch/bleve/v2/analysis/token/edgengram" // Register edge_ngram filter
	_ "github.com/blevesearch/bleve/v2/analysis/token/lowercase" // Register lowercase filter
	_ "github.com/blevesearch/bleve/v2/analysis/token/ngram"     // Register ngram filter
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/chapmanjacobd/poisearch/internal/config"
)

// registerAnalyzers registers custom text analyzers with the index mapping.
// Supported analyzers:
//   - "standard": default Bleve analyzer (tokenizes on word boundaries, lowercases)
//   - "edge_ngram": produces prefix tokens (e.g., "matrix" -> "m", "ma", "mat", ...)
//   - "ngram": produces substring tokens of length 2-15 (e.g., "matrix" -> "ma", "at", "tr", ...)
//   - "keyword": no tokenization, exact match only
func registerAnalyzers(m *mapping.IndexMappingImpl) error {
	// Edge ngram analyzer: prefix tokens from the start of each word
	// "restaurant" -> "r", "re", "res", "rest", ... up to 20 chars
	if err := m.AddCustomTokenFilter("prefix_edge_ngram", map[string]any{
		"type": "edge_ngram",
		"min":  float64(1),
		"max":  float64(20),
		"back": false, // false = FRONT side (prefix)
	}); err != nil {
		return fmt.Errorf("register prefix_edge_ngram token filter: %w", err)
	}

	if err := m.AddCustomAnalyzer("edge_ngram", map[string]any{
		"type":          "custom",
		"tokenizer":     "unicode",
		"token_filters": []string{"to_lower", "prefix_edge_ngram"},
	}); err != nil {
		return fmt.Errorf("register edge_ngram analyzer: %w", err)
	}

	// Ngram analyzer: produces all substrings of length 2-15
	// "matrix" -> "ma", "at", "tr", "ri", "ix", "mat", "atr", ...
	if err := m.AddCustomTokenFilter("substring_ngram", map[string]any{
		"type": "ngram",
		"min":  float64(2),
		"max":  float64(15),
	}); err != nil {
		return fmt.Errorf("register substring_ngram token filter: %w", err)
	}

	if err := m.AddCustomAnalyzer("ngram", map[string]any{
		"type":          "custom",
		"tokenizer":     "unicode",
		"token_filters": []string{"to_lower", "substring_ngram"},
	}); err != nil {
		return fmt.Errorf("register ngram analyzer: %w", err)
	}

	return nil
}

func BuildIndexMapping(conf *config.Config) mapping.IndexMapping {
	indexMapping := bleve.NewIndexMapping()

	// Register custom analyzers
	if err := registerAnalyzers(indexMapping); err != nil {
		// Log warning but continue with standard analyzer
		fmt.Printf("warning: failed to register custom analyzers, falling back to standard: %v\n", err)
	}

	docMapping := bleve.NewDocumentMapping()

	// Disable _all field to save space
	docMapping.Enabled = true
	docMapping.Dynamic = false // Only index defined fields

	// Determine the name analyzer to use
	nameAnalyzer := conf.NameAnalyzer
	if nameAnalyzer == "" {
		nameAnalyzer = "standard" // default
	}

	// Name fields
	nameFieldMapping := bleve.NewTextFieldMapping()
	nameFieldMapping.Analyzer = nameAnalyzer
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

	// Multi-class fields (stored as comma-separated for filtering)
	docMapping.AddFieldMappingsAt("classes", keywordMapping)
	docMapping.AddFieldMappingsAt("subtypes", keywordMapping)

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

	// Address fields (opt-in via config)
	if conf.StoreAddress {
		addrMapping := bleve.NewTextFieldMapping()
		addrMapping.Analyzer = "keyword"
		addrMapping.IncludeInAll = false
		addrMapping.Store = true
		docMapping.AddFieldMappingsAt("addr:housenumber", addrMapping)
		docMapping.AddFieldMappingsAt("addr:street", addrMapping)
		docMapping.AddFieldMappingsAt("addr:city", addrMapping)
		docMapping.AddFieldMappingsAt("addr:postcode", addrMapping)
		docMapping.AddFieldMappingsAt("addr:country", addrMapping)
		docMapping.AddFieldMappingsAt("addr:state", addrMapping)
		docMapping.AddFieldMappingsAt("addr:district", addrMapping)
		docMapping.AddFieldMappingsAt("addr:suburb", addrMapping)
		docMapping.AddFieldMappingsAt("addr:neighbourhood", addrMapping)
	}

	indexMapping.DefaultMapping = docMapping
	return indexMapping
}

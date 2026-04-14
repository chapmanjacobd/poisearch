package search

import (
	"fmt"

	"github.com/blevesearch/bleve/v2"
	_ "github.com/blevesearch/bleve/v2/analysis/analyzer/custom"  // Register custom analyzer
	_ "github.com/blevesearch/bleve/v2/analysis/analyzer/keyword" // Register built-in keyword analyzer
	_ "github.com/blevesearch/bleve/v2/analysis/token/edgengram"  // Register edge_ngram filter
	_ "github.com/blevesearch/bleve/v2/analysis/token/lowercase"  // Register lowercase filter
	_ "github.com/blevesearch/bleve/v2/analysis/token/ngram"      // Register ngram filter
	_ "github.com/blevesearch/bleve/v2/analysis/tokenizer/single" // Register single tokenizer (for keyword)
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/chapmanjacobd/poisearch/internal/config"
)

// registerAnalyzers registers custom text analyzers with the index mapping.
func registerAnalyzers(m *mapping.IndexMappingImpl) error {
	// Edge ngram analyzer: prefix tokens from the start of each word
	if err := m.AddCustomTokenFilter("prefix_edge_ngram", map[string]any{
		"type": "edge_ngram",
		"min":  float64(1),
		"max":  float64(20),
		"back": false,
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

	// Case-insensitive keyword analyzer: treats entire field as single token, lowercased
	// Uses the 'single' tokenizer (entire input as one token) with lowercase filter
	if err := m.AddCustomAnalyzer("keyword", map[string]any{
		"type":          "custom",
		"tokenizer":     "single",
		"token_filters": []string{"to_lower"},
	}); err != nil {
		return fmt.Errorf("register keyword analyzer: %w", err)
	}

	return nil
}

// BuildIndexMapping creates the Bleve index mapping based on configuration.
func BuildIndexMapping(conf *config.Config) mapping.IndexMapping {
	indexMapping := bleve.NewIndexMapping()

	if err := registerAnalyzers(indexMapping); err != nil {
		fmt.Printf("warning: failed to register custom analyzers, falling back to standard: %v\n", err)
	}

	docMapping := bleve.NewDocumentMapping()
	docMapping.Enabled = true
	docMapping.Dynamic = false

	nameAnalyzer := conf.NameAnalyzer
	if nameAnalyzer == "" {
		nameAnalyzer = "standard"
	}

	addNameFields(docMapping, conf, nameAnalyzer)
	addClassSubtypeFields(docMapping, conf)
	addImportanceField(docMapping, conf)
	addGeometryField(docMapping, conf)
	addAddressFields(docMapping, conf)
	addMetadataFields(docMapping, conf)
	addWikidataRedirectFields(docMapping, conf, nameAnalyzer)

	indexMapping.DefaultMapping = docMapping
	return indexMapping
}

func addMetadataFields(docMapping *mapping.DocumentMapping, _ *config.Config) {
	keywordMapping := bleve.NewTextFieldMapping()
	keywordMapping.Analyzer = "keyword"
	keywordMapping.IncludeInAll = false
	keywordMapping.Store = true

	fields := []string{"phone", "wheelchair", "opening_hours"}
	for _, f := range fields {
		docMapping.AddFieldMappingsAt(f, keywordMapping)
	}
}

func addNameFields(docMapping *mapping.DocumentMapping, conf *config.Config, nameAnalyzer string) {
	nameFieldMapping := bleve.NewTextFieldMapping()
	nameFieldMapping.Analyzer = nameAnalyzer
	nameFieldMapping.IncludeInAll = true
	nameFieldMapping.IncludeTermVectors = false
	nameFieldMapping.Store = true

	fields := []string{"name", "alt_name", "old_name", "short_name", "brand", "operator"}
	for _, f := range fields {
		docMapping.AddFieldMappingsAt(f, nameFieldMapping)
	}

	for _, lang := range conf.Languages {
		docMapping.AddFieldMappingsAt("name:"+lang, nameFieldMapping)
		docMapping.AddFieldMappingsAt("alt_name:"+lang, nameFieldMapping)
		docMapping.AddFieldMappingsAt("old_name:"+lang, nameFieldMapping)
		docMapping.AddFieldMappingsAt("short_name:"+lang, nameFieldMapping)
	}
}

func addClassSubtypeFields(docMapping *mapping.DocumentMapping, conf *config.Config) {
	keywordMapping := bleve.NewTextFieldMapping()
	keywordMapping.Analyzer = "keyword"
	keywordMapping.IncludeInAll = true
	keywordMapping.IncludeTermVectors = false
	keywordMapping.Store = conf.StoreMetadata

	docMapping.AddFieldMappingsAt("class", keywordMapping)
	docMapping.AddFieldMappingsAt("subtype", keywordMapping)
	docMapping.AddFieldMappingsAt("classes", keywordMapping)
	docMapping.AddFieldMappingsAt("subtypes", keywordMapping)
}

func addImportanceField(docMapping *mapping.DocumentMapping, conf *config.Config) {
	numMapping := bleve.NewNumericFieldMapping()
	numMapping.IncludeInAll = false
	numMapping.Store = conf.StoreMetadata
	docMapping.AddFieldMappingsAt("importance", numMapping)
}

func addGeometryField(docMapping *mapping.DocumentMapping, conf *config.Config) {
	geoMode := conf.GeometryMode
	if geoMode == "" || geoMode == "no-geo" {
		return
	}

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

func addAddressFields(docMapping *mapping.DocumentMapping, conf *config.Config) {
	if !conf.StoreAddress {
		return
	}

	addrMapping := bleve.NewTextFieldMapping()
	addrMapping.Analyzer = "keyword"
	addrMapping.IncludeInAll = false
	addrMapping.Store = true

	fields := []string{
		"addr:housenumber", "addr:street", "addr:city", "addr:postcode",
		"addr:country", "addr:state", "addr:district", "addr:suburb",
		"addr:neighbourhood", "addr:floor", "addr:unit", "level",
	}
	for _, f := range fields {
		docMapping.AddFieldMappingsAt(f, addrMapping)
	}
}

func addWikidataRedirectFields(docMapping *mapping.DocumentMapping, conf *config.Config, nameAnalyzer string) {
	if !conf.IndexWikidataRedirects {
		return
	}

	wdRedirectMapping := bleve.NewTextFieldMapping()
	wdRedirectMapping.Analyzer = nameAnalyzer
	wdRedirectMapping.IncludeInAll = true
	wdRedirectMapping.IncludeTermVectors = false
	wdRedirectMapping.Store = true
	docMapping.AddFieldMappingsAt("wikidata_redirects", wdRedirectMapping)
}

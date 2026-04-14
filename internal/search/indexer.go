package search

import (
	"errors"
	"fmt"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
)

type Feature struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Names             map[string]string `json:"names"` // name:en, name:zh, etc.
	Class             string            `json:"class"`
	Subtype           string            `json:"subtype"`
	Classes           []string          `json:"classes,omitempty"`  // multi-class support
	Subtypes          []string          `json:"subtypes,omitempty"` // multi-class support
	Importance        float64           `json:"importance"`
	Geometry          any               `json:"geometry"`
	Address           map[string]string `json:"address,omitempty"`            // addr:housenumber, addr:street, etc.
	WikidataRedirects []string          `json:"wikidata_redirects,omitempty"` // Wikipedia redirect titles for this QID
}

func OpenOrCreateIndex(indexPath string, m mapping.IndexMapping) (bleve.Index, error) {
	index, err := bleve.Open(indexPath)
	if errors.Is(err, bleve.ErrorIndexPathDoesNotExist) {
		index, err = bleve.New(indexPath, m)
		if err != nil {
			return nil, fmt.Errorf("could not create new index: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("could not open existing index: %w", err)
	}
	return index, nil
}

func (f *Feature) Type() string {
	return "poi"
}

func FeatureToMap(f *Feature) map[string]any {
	// Note: We do NOT store "id" in the document body.
	// Bleve already stores it as the primary key of the document.
	m := map[string]any{
		"name":       f.Name,
		"class":      f.Class,
		"subtype":    f.Subtype,
		"importance": f.Importance,
		"geometry":   f.Geometry,
	}
	for k, v := range f.Names {
		m[k] = v
	}
	// Store multi-class fields for filtering
	if len(f.Classes) > 0 {
		m["classes"] = f.Classes
	}
	if len(f.Subtypes) > 0 {
		m["subtypes"] = f.Subtypes
	}
	// Store address fields when configured
	if len(f.Address) > 0 {
		for k, v := range f.Address {
			m[k] = v
		}
	}
	// Store wikidata redirect titles when configured
	if len(f.WikidataRedirects) > 0 {
		m["wikidata_redirects"] = strings.Join(f.WikidataRedirects, "|")
	}
	return m
}

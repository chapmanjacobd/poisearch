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
	Key             string            `json:"key"`
	Value           string            `json:"value"`
	Keys           []string          `json:"keys,omitempty"`  // multi-key support
	Values          []string          `json:"values,omitempty"` // multi-key support
	Importance        float64           `json:"importance"`
	Geometry          any               `json:"geometry"`
	Address           map[string]string `json:"address,omitempty"`            // addr:housenumber, addr:street, etc.
	WikidataRedirects []string          `json:"wikidata_redirects,omitempty"` // Wikipedia redirect titles for this QID
	Phone             string            `json:"phone,omitempty"`
	Wheelchair        string            `json:"wheelchair,omitempty"`
	OpeningHours      string            `json:"opening_hours,omitempty"`
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
		"key":      f.Key,
		"value":    f.Value,
		"importance": f.Importance,
		"geometry":   f.Geometry,
	}
	for k, v := range f.Names {
		m[k] = v
	}
	// Store multi-key fields for filtering
	if len(f.Keys) > 0 {
		m["keys"] = f.Keys
	}
	if len(f.Values) > 0 {
		m["values"] = f.Values
	}
	// Store address fields when configured
	if len(f.Address) > 0 {
		for k, v := range f.Address {
			m[k] = v
		}
	}
	if f.Phone != "" {
		m["phone"] = f.Phone
	}
	if f.Wheelchair != "" {
		m["wheelchair"] = f.Wheelchair
	}
	if f.OpeningHours != "" {
		m["opening_hours"] = f.OpeningHours
	}
	// Store wikidata redirect titles when configured
	if len(f.WikidataRedirects) > 0 {
		m["wikidata_redirects"] = strings.Join(f.WikidataRedirects, "|")
	}
	return m
}

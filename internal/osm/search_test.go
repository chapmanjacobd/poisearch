package osm

import (
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/search"
)

func TestMatchTextQuery(t *testing.T) {
	tests := []struct {
		name       string
		tags       map[string]string
		params     search.SearchParams
		queryLower string
		expected   bool
	}{
		{
			name:       "match name",
			tags:       map[string]string{"name": "McDonald's"},
			params:     search.SearchParams{Query: "mcdonald"},
			queryLower: "mcdonald",
			expected:   true,
		},
		{
			name:       "match alt_name",
			tags:       map[string]string{"name": "Fast Food", "alt_name": "Micky D"},
			params:     search.SearchParams{Query: "micky"},
			queryLower: "micky",
			expected:   true,
		},
		{
			name:       "match brand",
			tags:       map[string]string{"name": "Fast Food", "brand": "McDonald's"},
			params:     search.SearchParams{Query: "mcdonald"},
			queryLower: "mcdonald",
			expected:   true,
		},
		{
			name:       "arbitrary data match (e.g. description)",
			tags:       map[string]string{"name": "Fast Food", "description": "Cheap burgers"},
			params:     search.SearchParams{Query: "burger"},
			queryLower: "burger",
			expected:   true,
		},
		{
			name:       "no match",
			tags:       map[string]string{"name": "McDonald's"},
			params:     search.SearchParams{Query: "burger"},
			queryLower: "burger",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchTextQuery(tt.tags, tt.params, tt.queryLower)
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestMatchMetadata(t *testing.T) {
	tests := []struct {
		name     string
		tags     map[string]string
		params   search.SearchParams
		expected bool
	}{
		{
			name:     "phone match",
			tags:     map[string]string{"phone": "+49 30 123456"},
			params:   search.SearchParams{Phone: "123456"},
			expected: true,
		},
		{
			name:     "contact:phone match",
			tags:     map[string]string{"contact:phone": "+49 30 123456"},
			params:   search.SearchParams{Phone: "123456"},
			expected: true,
		},
		{
			name:     "wheelchair match",
			tags:     map[string]string{"wheelchair": "yes"},
			params:   search.SearchParams{Wheelchair: "yes"},
			expected: true,
		},
		{
			name:     "opening_hours match",
			tags:     map[string]string{"opening_hours": "24/7"},
			params:   search.SearchParams{OpeningHours: "24/7"},
			expected: true,
		},
		{
			name:     "no metadata match",
			tags:     map[string]string{"phone": "111111"},
			params:   search.SearchParams{Phone: "222222"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchMetadata(tt.tags, tt.params)
			if got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

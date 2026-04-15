package osm_test

import (
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/osm"
)

func TestEnhanceName(t *testing.T) {
	tests := []struct {
		name     string
		tags     map[string]string
		expected string
	}{
		{
			name:     "no brand/operator",
			tags:     map[string]string{"name": "McDonald's"},
			expected: "McDonald's",
		},
		{
			name:     "brand not in name",
			tags:     map[string]string{"name": "Fast Food", "brand": "McDonald's"},
			expected: "Fast Food (McDonald's)",
		},
		{
			name:     "brand already in name",
			tags:     map[string]string{"name": "McDonald's Berlin", "brand": "McDonald's"},
			expected: "McDonald's Berlin",
		},
		{
			name:     "brand in name case-insensitive",
			tags:     map[string]string{"name": "mcdonald's Berlin", "brand": "McDonald's"},
			expected: "mcdonald's Berlin",
		},
		{
			name:     "operator not in name",
			tags:     map[string]string{"name": "Local Bus", "operator": "BVG"},
			expected: "Local Bus (BVG)",
		},
		{
			name:     "religion and denomination",
			tags:     map[string]string{"name": "St. Mary's", "religion": "christian", "denomination": "catholic"},
			expected: "St. Mary's (christian)", // EnhanceName returns after first match
		},
		{
			name: "religion in name",
			tags: map[string]string{
				"name":         "St. Mary's Christian Church",
				"religion":     "christian",
				"denomination": "catholic",
			},
			expected: "St. Mary's Christian Church (catholic)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			osm.EnhanceName(tt.tags)
			if tt.tags["name"] != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.tags["name"])
			}
		})
	}
}

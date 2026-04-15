//nolint:testpackage // Tests need access to internal functions
package osm

import (
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/config"
)

func TestMatchBoostPattern(t *testing.T) {
	tests := []struct {
		class, subtype, pattern string
		expected                bool
	}{
		{"amenity", "hospital", "hospital", true},
		{"hospital", "yes", "hospital", true},
		{"amenity", "pharmacy", "amenity=pharmacy", true},
		{"shop", "pharmacy", "amenity=pharmacy", false},
		{"hospital", "clinic", "hospital=*", true},
		{"hospital", "clinic", "hospital=", true},
		{"amenity", "hospital", "hospital=*", false},
		{"amenity", "hospital", "*=hospital", true},
		{"amenity", "hospital", "=hospital", true},
		{"place", "city", "city", true},
		{"place", "city", "place=city", true},
		{"place", "village", "place=city", false},
	}

	for _, tt := range tests {
		got := matchBoostPattern(tt.class, tt.subtype, tt.pattern)
		if got != tt.expected {
			t.Errorf("matchBoostPattern(%s, %s, %s) = %v, want %v", tt.class, tt.subtype, tt.pattern, got, tt.expected)
		}
	}
}

func TestClassify(t *testing.T) {
	weights := &config.ImportanceWeights{
		Boosts:   []string{"city", "amenity=pharmacy", "*=big", "hospital"},
		Default:  1.0,
		PopBoost: 1.0,
		Capital:  2.0,
		Wiki:     1.5,
	}

	tests := []struct {
		name     string
		tags     map[string]string
		expected *Classification
	}{
		{
			name: "City (matches boost 'city')",
			tags: map[string]string{"place": "city"},
			expected: &Classification{
				Class:      "place",
				Subtype:    "city",
				Importance: 1040.0,
			},
		},
		{
			name: "Pharmacy (matches boost 'amenity=pharmacy')",
			tags: map[string]string{"amenity": "pharmacy"},
			expected: &Classification{
				Class:      "amenity",
				Subtype:    "pharmacy",
				Importance: 1030.0,
			},
		},
		{
			name: "Any=big (matches boost '*=big')",
			tags: map[string]string{"shop": "big"},
			expected: &Classification{
				Class:      "shop",
				Subtype:    "big",
				Importance: 1020.0,
			},
		},
		{
			name: "Hospital (matches boost 'hospital')",
			tags: map[string]string{"healthcare": "hospital"},
			expected: &Classification{
				Class:      "healthcare",
				Subtype:    "hospital",
				Importance: 1010.0,
			},
		},
		{
			name: "Restaurant (unboosted fallback to default)",
			tags: map[string]string{"amenity": "restaurant"},
			expected: &Classification{
				Class:      "amenity",
				Subtype:    "restaurant",
				Importance: 2.0,
			},
		},
		{
			name: "City with population boost",
			tags: map[string]string{"place": "city", "population": "1000"},
			expected: &Classification{
				Class:      "place",
				Subtype:    "city",
				Importance: 1040.0 + 6.90875, // 1040 + ln(1001) ≈ 1040 + 6.90875
			},
		},
		{
			name:     "Unknown",
			tags:     map[string]string{"foo": "bar"},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.tags, weights)
			if tt.expected == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected %v, got nil", tt.expected)
			}
			if got.Class != tt.expected.Class || got.Subtype != tt.expected.Subtype {
				t.Errorf(
					"expected class/subtype %s/%s, got %s/%s",
					tt.expected.Class,
					tt.expected.Subtype,
					got.Class,
					got.Subtype,
				)
			}
			if (got.Importance - tt.expected.Importance) > 0.01 {
				t.Errorf("expected importance %f, got %f", tt.expected.Importance, got.Importance)
			}
		})
	}
}

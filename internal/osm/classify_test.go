//nolint:testpackage // Tests need access to internal functions
package osm

import (
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/config"
)

func TestClassify(t *testing.T) {
	weights := &config.ImportanceWeights{
		Place:    map[string]float64{"city": 5.0, "town": 4.0},
		Amenity:  map[string]float64{"restaurant": 2.0},
		Default:  1.0,
		PopBoost: 5.0,
		Capital:  2.0,
		Wiki:     1.5,
	}

	tests := []struct {
		name     string
		tags     map[string]string
		expected *Classification
	}{
		{
			name: "City with population",
			tags: map[string]string{"place": "city", "population": "1000000"},
			expected: &Classification{
				Class:      "place",
				Subtype:    "city",
				Importance: 5.0 + 69.077558, // 5 + ln(1000001) * 5 ≈ 5 + 13.8155 * 5 ≈ 5 + 69.0776
			},
		},
		{
			name: "Restaurant",
			tags: map[string]string{"amenity": "restaurant"},
			expected: &Classification{
				Class:      "amenity",
				Subtype:    "restaurant",
				Importance: 2.0,
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

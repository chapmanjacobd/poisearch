package osm_test

import (
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/osm"
)

func TestNormalizeNameTag(t *testing.T) {
	tests := []struct {
		name      string
		tags      map[string]string
		languages []string
		want      string
	}{
		{
			name:      "already has name",
			tags:      map[string]string{"name": "Berlin", "name:en": "Berlin EN"},
			languages: []string{"en"},
			want:      "Berlin",
		},
		{
			name:      "no name, use preferred language",
			tags:      map[string]string{"name:de": "Berlin DE", "name:en": "Berlin EN"},
			languages: []string{"de", "en"},
			want:      "Berlin DE",
		},
		{
			name:      "no name, use second preferred language",
			tags:      map[string]string{"name:fr": "Berlin FR", "name:en": "Berlin EN"},
			languages: []string{"de", "fr", "en"},
			want:      "Berlin FR",
		},
		{
			name:      "no name, use en fallback",
			tags:      map[string]string{"name:ru": "Берлин", "name:en": "Berlin EN"},
			languages: []string{"de", "fr"},
			want:      "Berlin EN",
		},
		{
			name:      "no name, no preferred, no en fallback",
			tags:      map[string]string{"name:ru": "Берлин"},
			languages: []string{"de", "fr"},
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			osm.NormalizeNameTag(tt.tags, tt.languages)
			if got := tt.tags["name"]; got != tt.want {
				t.Errorf("NormalizeNameTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

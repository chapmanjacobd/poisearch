package osm

import (
	"math"
	"strconv"

	"github.com/chapmanjacobd/poisearch/internal/config"
)

type Classification struct {
	Class      string
	Subtype    string
	Importance float64
}

func Classify(tags map[string]string, weights *config.ImportanceWeights) *Classification {
	// Priority order for identifying the "primary" purpose of an OSM object.
	priorityKeys := []string{
		"place", "amenity", "shop", "tourism", "leisure",
		"historic", "natural", "railway", "aeroway", "highway",
		"boundary", "landuse",
	}

	var class, subtype string
	var importance float64

	for _, k := range priorityKeys {
		v, ok := tags[k]
		if !ok || v == "" || v == "yes" || v == "no" {
			continue
		}

		class = k
		subtype = v
		importance = weights.Default

		// Map OSM keys to our simplified classes
		switch k {
		case "highway":
			class = "street"
		}

		// Look up specific weights for this class/subtype
		foundWeight := false
		switch k {
		case "place":
			if w, ok := weights.Place[v]; ok {
				importance = w
				foundWeight = true
			} else {
				// We usually only index places if they are in our whitelist (cities, towns, etc.)
				return nil
			}
		case "amenity":
			if w, ok := weights.Amenity[v]; ok {
				importance = w
				foundWeight = true
			}
		case "highway":
			if w, ok := weights.Highway[v]; ok {
				importance = w
				foundWeight = true
			}
		case "shop":
			if w, ok := weights.Shop[v]; ok {
				importance = w
				foundWeight = true
			}
		case "tourism":
			if w, ok := weights.Tourism[v]; ok {
				importance = w
				foundWeight = true
			}
		case "leisure":
			if w, ok := weights.Leisure[v]; ok {
				importance = w
				foundWeight = true
			}
		case "historic":
			if w, ok := weights.Historic[v]; ok {
				importance = w
				foundWeight = true
			}
		case "natural":
			if w, ok := weights.Natural[v]; ok {
				importance = w
				foundWeight = true
			}
		case "railway":
			if w, ok := weights.Railway[v]; ok {
				importance = w
				foundWeight = true
			}
		}

		// Apply global boosts
		if !foundWeight {
			importance = weights.Default
		}

		// Population boost: importance += ln(pop+1) * factor
		if weights.PopBoost > 0 {
			if popStr, ok := tags["population"]; ok {
				if pop, err := strconv.Atoi(popStr); err == nil {
					importance += math.Log(float64(pop)+1) * weights.PopBoost
				}
			}
		}

		// Capital boost
		if tags["capital"] == "yes" {
			importance += weights.Capital
		}

		// Wikipedia boost (any POI with a wikipedia or wikidata tag gets a boost)
		if tags["wikipedia"] != "" || tags["wikidata"] != "" {
			importance += weights.Wiki
		}

		return &Classification{
			Class:      class,
			Subtype:    subtype,
			Importance: importance,
		}
	}

	return nil
}

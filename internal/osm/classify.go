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
	var class, subtype string
	var importance float64

	if v, ok := tags["place"]; ok {
		if w, ok := weights.Place[v]; ok {
			importance = w
		} else {
			// If not in importance map, we might still want to index it?
			// The user's original code returned "", "", 0 if not city/town/village.
			// Let's stick to the weight map.
			return nil
		}
		class = "place"
		subtype = v

		// population boost
		if popStr, ok := tags["population"]; ok {
			if pop, err := strconv.Atoi(popStr); err == nil {
				importance += math.Log(float64(pop)+1) / weights.PopBoost
			}
		}

		// capital boost
		if tags["capital"] == "yes" {
			importance += weights.Capital
		}

		// wikipedia boost
		if _, ok := tags["wikipedia"]; ok {
			importance += weights.Wiki
		}

		return &Classification{Class: class, Subtype: subtype, Importance: importance}
	}

	if v, ok := tags["amenity"]; ok {
		if w, ok := weights.Amenity[v]; ok {
			importance = w
		} else {
			importance = weights.Default
		}
		return &Classification{Class: "amenity", Subtype: v, Importance: importance}
	}

	if v, ok := tags["highway"]; ok {
		if w, ok := weights.Highway[v]; ok {
			importance = w
		} else {
			importance = weights.Default
		}
		return &Classification{Class: "street", Subtype: v, Importance: importance}
	}

	return nil
}

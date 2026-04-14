package osm

import (
	"math"
	"strconv"
	"strings"

	"github.com/chapmanjacobd/poisearch/internal/config"
)

type Classification struct {
	Class      string
	Subtype    string
	Importance float64
	OntLevel   int // Ontological level (0-6), -1 if not applicable
}

// ClassifyMulti returns all applicable classifications for a set of OSM tags.
// A single POI can have multiple classifications (e.g., a building that is both
// a historic castle and a tourism museum).
//
//nolint:revive,cyclop,funlen // Classification requires checking multiple OSM keys and applying various boosts
func ClassifyMulti(
	tags map[string]string,
	weights *config.ImportanceWeights,
	ont *PlaceTypeOntology,
) []*Classification {
	// All keys that can contribute to classification
	classifiableKeys := []string{
		"place", "amenity", "shop", "tourism", "leisure",
		"historic", "natural", "railway", "aeroway", "highway",
		"boundary", "landuse",
	}

	var results []*Classification

	for _, k := range classifiableKeys {
		v, ok := tags[k]
		if !ok || v == "" || v == "yes" || v == "no" {
			continue
		}

		class := k
		subtype := v
		importance := weights.Default

		// Map OSM keys to our simplified classes
		if k == "highway" {
			class = "street"
		}

		// Look up specific weights for this class/subtype
		foundWeight := false
		switch k {
		case "place":
			w, ok := weights.Place[v]
			if !ok {
				// Skip places not in our whitelist
				continue
			}
			importance = w
			foundWeight = true
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
			// Use type-based default importance when no explicit weight is configured
			importance = getTypeDefaultImportance(k, v)
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

		// Ontology boost: boost based on ontological level
		ontLevel := -1
		if ont != nil {
			ontLevel = ont.GetMinLevelForOSM(k, v)
			if ontLevel >= 0 {
				importance *= ont.BoostByOntology(ontLevel)
			}
		}

		results = append(results, &Classification{
			Class:      class,
			Subtype:    subtype,
			Importance: importance,
			OntLevel:   ontLevel,
		})
	}

	return results
}

// Classify returns the first (highest priority) classification for a set of OSM tags.
// Kept for backward compatibility. Use ClassifyMulti for multi-class support.
func Classify(tags map[string]string, weights *config.ImportanceWeights) *Classification {
	return ClassifyWithOntology(tags, weights, nil)
}

// ClassifyWithOntology returns the highest-importance classification with ontology support.
func ClassifyWithOntology(
	tags map[string]string,
	weights *config.ImportanceWeights,
	ont *PlaceTypeOntology,
) *Classification {
	results := ClassifyMulti(tags, weights, ont)
	if len(results) == 0 {
		return nil
	}
	// Return the classification with highest importance
	best := results[0]
	for _, r := range results[1:] {
		if r.Importance > best.Importance {
			best = r
		}
	}
	return best
}

// ParseClasses parses a comma-separated class filter string into a slice.
// Supports wildcard syntax: "shop=*" returns class "shop" with any subtype.
func ParseClasses(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// ParseSubtypes parses a comma-separated subtype filter string into a slice.
func ParseSubtypes(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// getTypeDefaultImportance returns a sensible default importance based on OSM key/value
// when no explicit weight is configured in the config file.
// This provides reasonable defaults for common place types.
func getTypeDefaultImportance(key, value string) float64 {
	switch key {
	case "place":
		switch value {
		case "city", "town":
			return 4.0
		case "village", "hamlet":
			return 3.0
		case "suburb", "quarter", "neighbourhood":
			return 2.5
		default:
			return 2.0
		}
	case "amenity":
		return 2.0
	case "shop":
		return 1.5
	case "tourism", "leisure":
		return 1.5
	case "highway":
		return 1.0
	case "historic", "natural":
		return 1.5
	case "railway":
		return 1.5
	case "boundary":
		return 2.0
	default:
		return 0.5
	}
}

// parsePopulation extracts population value from tags for tie-breaking.
func parsePopulation(tags map[string]string) int {
	if popStr, ok := tags["population"]; ok {
		if pop, err := strconv.Atoi(popStr); err == nil {
			return pop
		}
	}
	return 0
}

// parsePopulationForClassification is a placeholder for classification-specific population.
// Currently uses the same population tag since OSM doesn't have per-class populations.
func parsePopulationForClassification(class, subtype string, tags map[string]string) int {
	_ = class // Currently unused, reserved for future per-class population logic
	_ = subtype
	return parsePopulation(tags)
}

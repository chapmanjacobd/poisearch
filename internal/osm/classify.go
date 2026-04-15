package osm

import (
	"math"
	"strconv"
	"strings"

	"github.com/chapmanjacobd/poisearch/internal/config"
)

type Classification struct {
	Key        string
	Value      string
	Importance float64
	OntLevel   int // Ontological level (0-6), -1 if not applicable
}

// ClassifyMulti returns all applicable classifications for a set of OSM tags.
// A single POI can have multiple classifications (e.g., a building that is both
// a historic castle and a tourism museum).
//
//nolint:revive // Classification requires checking multiple OSM keys and applying various boosts
func ClassifyMulti(
	tags map[string]string,
	weights *config.ImportanceWeights,
	ont *PlaceTypeOntology,
) []*Classification {
	// All keys that can contribute to classification
	classifiableKeys := []string{
		"place", "amenity", "shop", "tourism", "leisure",
		"historic", "natural", "railway", "aeroway", "highway",
		"boundary", "landuse", "building", "office", "man_made",
		"craft", "military", "healthcare", "public_transport",
		"power", "industrial", "emergency", "aerialway", "bridge",
		"club", "government", "information", "junction", "parking",
		"playground", "ruins", "social_facility", "sport", "studio",
		"traffic_calming", "cuisine", "religion", "attraction",
		"brewery", "castle_type", "consulate", "crossing",
		"denomination", "denotation", "diplomatic", "garden:type",
		"harbour:category", "healthcare:speciality", "military_service",
		"operator:type", "power_supply", "recycling_type", "residential",
		"sanitary_dump_station", "service", "shelter_type",
		"social_facility:for", "surface", "theatre:type",
		"toilets:access", "tower:type", "aerodrome:type",
		"vending", "waterway", "water", "artwork_type",
		"building:use", "clothes", "fuel", "route", "waste",
		"internet_access", "wheelchair",
		"addr:housenumber", "addr:street", "addr:postcode", "addr:city",
		"brand", "operator",
		"ref", "int_ref", "nat_ref", "reg_ref",
		"official_name", "loc_name", "reg_name",
		"addr:suburb", "addr:neighbourhood", "addr:district", "addr:state", "addr:province",
	}

	var results []*Classification

	for _, k := range classifiableKeys {
		v, ok := tags[k]
		if !ok || v == "" || v == "yes" || v == "no" {
			continue
		}

		key := k
		value := v

		// Map OSM keys to our simplified keys
		if k == "highway" {
			key = "street"
		}

		// Calculate base importance
		importance := getTypeDefaultImportance(k, v)
		boostFound := false
		for i, pattern := range weights.Boosts {
			if matchBoostPattern(key, value, pattern) {
				// Strict bifurcation: boosted items get 1000+ score to sort above non-boosted matches.
				// First match in the array gets the highest priority.
				importance = 1000.0 + float64(len(weights.Boosts)-i)*10.0
				boostFound = true
				break
			}
		}

		if !boostFound && weights.Default != 0 && weights.Default != 1.0 {
			importance *= weights.Default
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
			Key:        key,
			Value:      value,
			Importance: importance,
			OntLevel:   ontLevel,
		})
	}

	return results
}

// matchBoostPattern evaluates if a key/value pair matches a boost pattern.
// Supported patterns:
//   - "hospital"         : Match if key="hospital" OR value="hospital"
//   - "amenity=hospital" : Match key="amenity" AND value="hospital"
//   - "hospital=*"       : Match key="hospital" AND any value
//   - "hospital="        : Match key="hospital" AND any value
//   - "*=big" or "=big"  : Match any key AND value="big"
func matchBoostPattern(key, value, pattern string) bool {
	if strings.Contains(pattern, "=") {
		parts := strings.SplitN(pattern, "=", 2)
		pKey := parts[0]
		pValue := parts[1]

		matchKey := pKey == "" || pKey == "*" || pKey == key
		matchValue := pValue == "" || pValue == "*" || pValue == value
		return matchKey && matchValue
	}
	// No '=', match if pattern equals key or pattern equals value
	return pattern == key || pattern == value
}

// Classify returns the first (highest priority) classification for a set of OSM tags.
// Kept for backward compatibility. Use ClassifyMulti for multi-key support.
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

// ParseKeys parses a comma-separated key filter string into a slice.
// Supports wildcard syntax: "shop=*" returns key "shop" with any value.
func ParseKeys(s string) []string {
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

// ParseValues parses a comma-separated value filter string into a slice.
func ParseValues(s string) []string {
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
	case "historic", "natural", "waterway", "water":
		return 1.5
	case "railway":
		return 1.5
	case "boundary":
		return 2.0
	case "building", "office", "man_made", "craft", "military", "healthcare", "cuisine", "religion":
		return 1.5
	case "public_transport",
		"power",
		"industrial",
		"emergency",
		"attraction",
		"brewery",
		"consulate",
		"route",
		"artwork_type":
		return 1.2
	case "aerialway", "bridge", "club", "government", "information", "junction", "residential":
		return 1.0
	case "parking",
		"playground",
		"ruins",
		"social_facility",
		"sport",
		"studio",
		"traffic_calming",
		"vending",
		"building:use",
		"clothes",
		"fuel",
		"waste",
		"internet_access",
		"wheelchair",
		"addr:housenumber",
		"addr:street",
		"addr:postcode",
		"addr:city",
		"addr:suburb",
		"addr:neighbourhood",
		"addr:district",
		"addr:state",
		"addr:province",
		"brand",
		"operator",
		"ref",
		"int_ref",
		"nat_ref",
		"reg_ref",
		"official_name",
		"loc_name",
		"reg_name":
		return 1.0
	case "castle_type",
		"crossing",
		"denomination",
		"denotation",
		"diplomatic",
		"garden:type",
		"harbour:category",
		"healthcare:speciality",
		"military_service",
		"operator:type",
		"power_supply",
		"recycling_type",
		"sanitary_dump_station",
		"service",
		"shelter_type",
		"social_facility:for",
		"surface",
		"theatre:type",
		"toilets:access",
		"tower:type",
		"aerodrome:type":
		return 0.8
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
// Currently uses the same population tag since OSM doesn't have per-key populations.
func parsePopulationForClassification(key, value string, tags map[string]string) int {
	_ = key // Currently unused, reserved for future per-key population logic
	_ = value
	return parsePopulation(tags)
}

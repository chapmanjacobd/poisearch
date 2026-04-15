package osm

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// PlaceTypeOntology maps Wikidata QIDs to their ontological level (0-6).
// Level 6 = finest granularity (e.g., individual buildings)
// Level 0 = coarsest granularity (e.g., continents, planets)
//
// This is inspired by the wikipedia-wikidata project's wikidata_place_type_levels.csv
// which provides a standardized hierarchy for place classification.
type PlaceTypeOntology struct {
	// qid -> ontological level (0-6)
	levels map[string]int
	// qid -> human-readable label
	labels map[string]string
	// Reverse mapping: OSM key/value -> list of QIDs
	osmToQIDs map[string][]string
	// Reverse mapping: label -> list of OSM key/value pairs
	labelToTags map[string][]TagMatch
}

type TagMatch struct {
	Key   string
	Value string
}

// DefaultOntology returns a built-in place type ontology based on common
// Wikidata place types and their OSM tag equivalents.
func DefaultOntology() *PlaceTypeOntology {
	ont := &PlaceTypeOntology{
		levels:      make(map[string]int),
		labels:      make(map[string]string),
		osmToQIDs:   make(map[string][]string),
		labelToTags: make(map[string][]TagMatch),
	}

	// Define place types with ontological levels
	// Format: QID, level, label, osm_key, osm_value
	types := []struct {
		qid    string
		level  int
		label  string
		osmKey string
		osmVal string
	}{
		// Administrative boundaries
		{"Q6256", 0, "country", "place", "country"},
		{"Q108681067", 1, "first-level administrative division", "boundary", "administrative"},
		{"Q20166006", 2, "second-level administrative division", "boundary", "administrative"},
		{"Q13220611", 3, "third-level administrative division", "boundary", "administrative"},
		{"Q24010666", 4, "fourth-level administrative division", "boundary", "administrative"},
		{"Q13414608", 5, "fifth-level administrative division", "boundary", "administrative"},
		{"Q22927291", 6, "sixth-level administrative division", "boundary", "administrative"},

		// Populated places (by size/importance)
		{"Q16377063", 1, "continent", "place", "continent"},
		{"Q515", 2, "city", "place", "city"},
		{"Q3957", 2, "town", "place", "town"},
		{"Q532", 3, "village", "place", "village"},
		{"Q486972", 3, "human settlement", "place", "hamlet"},
		{"Q3912148", 4, "neighbourhood", "place", "neighbourhood"},
		{"Q15097374", 4, "quarter", "place", "quarter"},

		// Natural features
		{"Q23442", 1, "planet", "natural", "planet"},
		{"Q4421", 2, "forest", "natural", "wood"},
		{"Q8502", 2, "mountain", "natural", "peak"},
		{"Q4022", 2, "river", "waterway", "river"},
		{"Q23397", 2, "lake", "natural", "water"},
		{"Q15324", 3, "ocean", "natural", "water"},
		{"Q3334556", 3, "beach", "natural", "beach"},
		{"Q16673529", 3, "valley", "natural", "valley"},

		// Man-made structures
		{"Q41176", 3, "building", "building", "yes"},
		{"Q44357", 3, "castle", "historic", "castle"},
		{"Q16917", 3, "hospital", "amenity", "hospital"},
		{"Q33506", 3, "museum", "tourism", "museum"},
		{"Q9842", 4, "primary school", "amenity", "school"},
		{"Q131734", 3, "university", "amenity", "university"},
		{"Q16560", 3, "station", "railway", "station"},
		{"Q1248784", 3, "airport", "aeroway", "aerodrome"},
		{"Q130003", 3, "ski resort", "leisure", "sports_centre"},
		{"Q41593339", 4, "playground", "leisure", "playground"},

		// Commercial
		{"Q27207358", 4, "shop", "shop", "yes"},
		{"Q11707", 3, "restaurant", "amenity", "restaurant"},
		{"Q30034223", 4, "cafe", "amenity", "cafe"},
		{"Q40231", 3, "hotel", "tourism", "hotel"},

		// Points of interest
		{"Q17347893", 4, "park", "leisure", "park"},
		{"Q5404044", 4, "monument", "historic", "monument"},
		{"Q133274", 3, "church", "building", "cathedral"},
		{"Q1255653", 3, "stadium", "leisure", "stadium"},
	}

	for _, t := range types {
		ont.levels[t.qid] = t.level
		ont.labels[t.qid] = t.label
		key := t.osmKey + "=" + t.osmVal
		ont.osmToQIDs[key] = append(ont.osmToQIDs[key], t.qid)
		ont.labelToTags[t.label] = append(ont.labelToTags[t.label], TagMatch{Key: t.osmKey, Value: t.osmVal})
	}

	return ont
}

// LoadOntologyFromCSV loads a place type ontology from a CSV file.
// Expected format: qid,ontological_level,label,osm_key,osm_value
func LoadOntologyFromCSV(path string) (*PlaceTypeOntology, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening ontology file: %w", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.Comment = '#'
	reader.FieldsPerRecord = -1 // Allow variable fields

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("reading CSV: %w", err)
	}

	ont := &PlaceTypeOntology{
		levels:      make(map[string]int),
		labels:      make(map[string]string),
		osmToQIDs:   make(map[string][]string),
		labelToTags: make(map[string][]TagMatch),
	}

	for _, record := range records {
		if len(record) < 5 {
			continue
		}

		qid := strings.TrimSpace(record[0])
		level, err := strconv.Atoi(strings.TrimSpace(record[1]))
		if err != nil || level < 0 || level > 6 {
			continue
		}
		label := strings.TrimSpace(record[2])
		osmKey := strings.TrimSpace(record[3])
		osmVal := strings.TrimSpace(record[4])

		ont.levels[qid] = level
		ont.labels[qid] = label
		key := osmKey + "=" + osmVal
		ont.osmToQIDs[key] = append(ont.osmToQIDs[key], qid)
		ont.labelToTags[label] = append(ont.labelToTags[label], TagMatch{Key: osmKey, Value: osmVal})
	}

	return ont, nil
}

// GetLevel returns the ontological level for a given QID.
// Returns -1 if the QID is not found.
func (p *PlaceTypeOntology) GetLevel(qid string) int {
	if p == nil {
		return -1
	}
	if level, ok := p.levels[qid]; ok {
		return level
	}
	return -1
}

// GetLabel returns the human-readable label for a given QID.
// Returns empty string if not found.
func (p *PlaceTypeOntology) GetLabel(qid string) string {
	if p == nil {
		return ""
	}
	return p.labels[qid]
}

// GetQIDsForOSM returns the list of Wikidata QIDs for a given OSM key/value pair.
func (p *PlaceTypeOntology) GetQIDsForOSM(osmKey, osmVal string) []string {
	if p == nil {
		return nil
	}
	key := osmKey + "=" + osmVal
	return p.osmToQIDs[key]
}

// GetMinLevelForOSM returns the minimum (most granular) ontological level
// for a given OSM key/value pair.
func (p *PlaceTypeOntology) GetMinLevelForOSM(osmKey, osmVal string) int {
	if p == nil {
		return -1
	}
	key := osmKey + "=" + osmVal
	qids := p.osmToQIDs[key]
	if len(qids) == 0 {
		return -1
	}

	minLevel := 7 // Higher than any valid level
	for _, qid := range qids {
		if level, ok := p.levels[qid]; ok && level < minLevel {
			minLevel = level
		}
	}
	return minLevel
}

// GetTagsForLabel returns the OSM key/value pairs for a given human-readable label.
func (p *PlaceTypeOntology) GetTagsForLabel(label string) []TagMatch {
	if p == nil {
		return nil
	}
	return p.labelToTags[strings.ToLower(label)]
}

// BoostByOntology returns a boost factor based on the ontological level.
// Finer granularity (higher level) = slightly higher boost for local relevance.
// Coarser granularity (lower level) = lower boost (more generic).
func (p *PlaceTypeOntology) BoostByOntology(level int) float64 {
	if level < 0 || level > 6 {
		return 1.0
	}
	// Scale: level 6 = 1.6x, level 0 = 1.0x
	return 1.0 + float64(level)*0.1
}

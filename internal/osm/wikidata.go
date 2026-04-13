package osm

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// WikidataImportance represents a Wikidata item's importance score.
// This is based on the wikimedia_importance.tsv.gz format from the
// wikipedia-wikidata ETL pipeline (Nominatim's importance ranking).
type WikidataImportance struct {
	Language    string  // Wikipedia language code (e.g., "en", "de")
	Type        string  // 'a' = article, 'r' = redirect
	Title       string  // Wikipedia article title
	Importance  float64 // Score from 0.0000000001 to 1.0
	WikidataID  string  // QID (e.g., "Q82425")
}

// WikidataLookup provides a way to look up Wikidata importance scores
// for OSM objects based on their wikidata tag value.
type WikidataLookup struct {
	// qid -> importance score (highest score if multiple entries)
	scores map[string]float64
}

// LoadWikidataImportance loads Wikidata importance scores from a TSV file.
// Supports both plain TSV and gzip-compressed TSV (.tsv.gz).
// Format: language\ttype\ttitle\timportance\twikidata_id
func LoadWikidataImportance(path string) (*WikidataLookup, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening wikidata importance file: %w", err)
	}
	defer f.Close()

	var reader io.Reader = f
	// Auto-detect gzip based on file extension
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("creating gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz
	} else if filepath.Ext(path) == ".tsv" {
		// For non-gzipped TSV, use the file directly
		reader = f
	}

	lookup := &WikidataLookup{
		scores: make(map[string]float64),
	}

	scanner := bufio.NewScanner(reader)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue // Skip empty lines and comments
		}

		parts := strings.Split(line, "\t")
		if len(parts) < 5 {
			continue // Skip malformed lines
		}

		// Parse the line
		wikidataID := strings.TrimSpace(parts[4])
		if !strings.HasPrefix(wikidataID, "Q") {
			continue // Skip non-QID entries
		}

		var importance float64
		fmt.Sscanf(parts[3], "%f", &importance)

		// Store the highest importance for each QID
		if existing, ok := lookup.scores[wikidataID]; !ok || importance > existing {
			lookup.scores[wikidataID] = importance
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading wikidata importance file: %w", err)
	}

	return lookup, nil
}

// GetImportance returns the Wikidata importance score for a given QID.
// Returns 0 if the QID is not found.
func (w *WikidataLookup) GetImportance(qid string) float64 {
	if w == nil || w.scores == nil {
		return 0
	}
	if score, ok := w.scores[qid]; ok {
		return score
	}
	return 0
}

// Size returns the number of QIDs in the lookup table.
func (w *WikidataLookup) Size() int {
	if w == nil {
		return 0
	}
	return len(w.scores)
}

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
	Language   string  // Wikipedia language code (e.g., "en", "de")
	Type       string  // 'a' = article, 'r' = redirect
	Title      string  // Wikipedia article title
	Importance float64 // Score from 0.0000000001 to 1.0
	WikidataID string  // QID (e.g., "Q82425")
}

// WikidataLookup provides a way to look up Wikidata importance scores
// for OSM objects based on their wikidata tag value.
type WikidataLookup struct {
	// qid -> highest importance score (backward compatible)
	scores map[string]float64
	// qid -> lang -> max importance score for that language
	langScores map[string]map[string]float64
	// qid -> list of redirect titles (Wikipedia redirects that point to this QID)
	redirects map[string][]string
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
		scores:     make(map[string]float64),
		langScores: make(map[string]map[string]float64),
		redirects:  make(map[string][]string),
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
		lang := strings.TrimSpace(parts[0])
		typ := strings.TrimSpace(parts[1])
		title := strings.TrimSpace(parts[2])
		wikidataID := strings.TrimSpace(parts[4])
		if !strings.HasPrefix(wikidataID, "Q") {
			continue // Skip non-QID entries
		}

		var importance float64
		_, _ = fmt.Sscanf(parts[3], "%f", &importance)

		// Store the highest importance for each QID (backward compatible)
		if existing, ok := lookup.scores[wikidataID]; !ok || importance > existing {
			lookup.scores[wikidataID] = importance
		}

		// Store per-language scores
		if _, ok := lookup.langScores[wikidataID]; !ok {
			lookup.langScores[wikidataID] = make(map[string]float64)
		}
		if existing, ok := lookup.langScores[wikidataID][lang]; !ok || importance > existing {
			lookup.langScores[wikidataID][lang] = importance
		}

		// Capture redirect titles (type == 'r')
		if typ == "r" && title != "" {
			lookup.redirects[wikidataID] = append(lookup.redirects[wikidataID], title)
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

// GetImportanceForLang returns the Wikidata importance score for a given QID,
// preferring the score for the specified language. Falls back to the highest
// score if the language-specific score is not found.
func (w *WikidataLookup) GetImportanceForLang(qid, lang string) float64 {
	if w == nil || w.langScores == nil {
		return 0
	}
	// Try language-specific score first
	if langScores, ok := w.langScores[qid]; ok {
		if score, ok := langScores[lang]; ok {
			return score
		}
	}
	// Fall back to highest score
	return w.GetImportance(qid)
}

// Size returns the number of QIDs in the lookup table.
func (w *WikidataLookup) Size() int {
	if w == nil {
		return 0
	}
	return len(w.scores)
}

// GetRedirects returns the list of Wikipedia redirect titles for a given QID.
// These can be used as alternate names to improve search discoverability.
// Returns nil if the QID has no redirects.
func (w *WikidataLookup) GetRedirects(qid string) []string {
	if w == nil || w.redirects == nil {
		return nil
	}
	return w.redirects[qid]
}

// RedirectCount returns the total number of redirect titles in the lookup table.
func (w *WikidataLookup) RedirectCount() int {
	if w == nil || w.redirects == nil {
		return 0
	}
	count := 0
	for _, redirects := range w.redirects {
		count += len(redirects)
	}
	return count
}

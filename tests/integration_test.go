package tests_test

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

// Test data directory (shared across tests to avoid re-downloading)
const testDataDir = ".."

// DefaultPBF is the default PBF filename used for testing.
const DefaultPBF = config.DefaultPBF

// downloadPBF downloads the default PBF if it doesn't exist.
// Returns the path to the PBF file.
func downloadPBF(t *testing.T) string {
	t.Helper()

	pbfURL := "http://download.geofabrik.de/europe/" + DefaultPBF
	pbfPath := filepath.Join(testDataDir, DefaultPBF)

	// Check if already exists
	if _, err := os.Stat(pbfPath); err == nil {
		return pbfPath
	}

	t.Logf("downlying PBF from %s...", pbfURL)

	// Download with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pbfURL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to download PBF: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download failed: HTTP %d", resp.StatusCode)
	}

	// Download to temp file first, then rename (atomic)
	tmpPath := pbfPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	_, err = io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmpPath)
		t.Fatalf("failed to write PBF: %v", err)
	}

	if err := os.Rename(tmpPath, pbfPath); err != nil {
		os.Remove(tmpPath)
		t.Fatalf("failed to rename PBF: %v", err)
	}

	t.Logf("downloaded PBF to %s", pbfPath)
	return pbfPath
}

// loadWikidataImportance loads wikidata importance if the file exists.
// Returns empty string if file not found.
func loadWikidataImportance(t *testing.T) string {
	t.Helper()

	path := "../wikimedia-importance-2025-11.csv.gz"
	if _, err := os.Stat(path); err != nil {
		t.Logf("wikimedia importance file not found at %s, skipping", path)
		return ""
	}
	return path
}

// buildTestIndex builds a Bleve index from a PBF file with the given configuration.
// Returns the index path and the opened index.
// The caller is responsible for closing the index and cleaning up the temp directory.
func buildTestIndex(t *testing.T, pbfPath string, conf *config.Config) (string, bleve.Index) {
	t.Helper()

	// Create temp directory for this test
	tempDir := t.TempDir()
	indexPath := filepath.Join(tempDir, "test.bleve")

	conf.IndexPath = indexPath
	conf.GeometryMode = "geopoint-centroid"
	conf.StoreMetadata = false
	conf.StoreGeometry = false

	// Load wikdata importance if configured
	if conf.WikidataImportance == "" {
		conf.WikidataImportance = loadWikidataImportance(t)
	}

	// Build index mapping
	m := search.BuildIndexMapping(conf)

	// Create or open index
	idx, err := search.OpenOrCreateIndex(indexPath, m)
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	// Build index from PBF
	err = osm.BuildIndex(pbfPath, conf, idx)
	if err != nil {
		idx.Close()
		t.Fatalf("failed to build index: %v", err)
	}

	return indexPath, idx
}

// defaultTestConfig returns a default configuration for tests.
func defaultTestConfig() *config.Config {
	return &config.Config{
		Languages: []string{"en", "de"},
		Importance: config.ImportanceWeights{
			// Broad filters to match any value for these tag keys
			Place: map[string]float64{
				"city": 5.0, "town": 4.0, "village": 3.0, "hamlet": 2.0,
				"suburb": 2.5, "country": 6.0, "state": 5.5,
				"county": 3.5, "municipality": 3.0, "borough": 2.5,
			},
			Amenity: map[string]float64{
				"restaurant": 2.0, "school": 1.5, "cafe": 1.5, "hospital": 2.0,
				"parking": 1.0, "bank": 1.0, "pharmacy": 1.0, "bar": 1.5,
				"fast_food": 1.5, "pub": 1.5, "library": 1.5, "townhall": 2.0,
				"marketplace": 1.5, "police": 1.5, "fire_station": 1.5,
				"post_office": 1.5, "courthouse": 1.5, "theatre": 1.5,
				"cinema": 1.5, "museum": 1.5, "place_of_worship": 1.5,
			},
			Shop: map[string]float64{
				"supermarket": 2.0, "bakery": 1.0, "clothes": 1.0,
				"convenience": 1.0, "mall": 1.5, "department_store": 1.5,
			},
			Highway: map[string]float64{
				"primary": 1.0, "secondary": 1.0, "tertiary": 1.0, "residential": 1.0,
				"unclassified": 1.0, "service": 1.0, "footway": 1.0,
			},
			Tourism: map[string]float64{
				"hotel": 1.5, "museum": 1.5, "attraction": 1.0,
				"guest_house": 1.0, "motel": 1.0, "hostel": 1.0,
				"information": 1.0, "viewpoint": 1.0,
			},
			Leisure: map[string]float64{
				"park": 1.0, "sports_centre": 1.0, "stadium": 1.5,
				"pitch": 1.0, "playground": 1.0,
			},
			Historic: map[string]float64{
				"castle": 1.5, "monument": 1.0, "ruins": 1.0,
				"archaeological_site": 1.0,
			},
			Natural: map[string]float64{
				"peak": 1.0, "water": 1.0, "wood": 1.0,
				"spring": 1.0,
			},
			Railway: map[string]float64{
				"station": 1.5, "halt": 1.0, "tram_stop": 1.0,
			},
			Default: 1.0,
		},
	}
}

// TestAnalyzer_Standard tests the standard analyzer (default behavior).
func TestAnalyzer_Standard(t *testing.T) {
	pbfPath := downloadPBF(t)
	conf := defaultTestConfig()
	conf.NameAnalyzer = "standard"

	_, idx := buildTestIndex(t, pbfPath, conf)
	defer idx.Close()

	tests := []struct {
		name      string
		query     string
		fuzzy     bool
		prefix    bool
		expectMin int
		expectMax int
	}{
		{"exact match: Vaduz", "Vaduz", false, false, 1, 100},
		{"exact match: Schaan", "Schaan", false, false, 1, 100},
		{"prefix search", "vad", false, true, 1, 100},
		{"prefix search: sch", "sch", false, true, 1, 100},
		{"fuzzy match", "Vadutz", true, false, 0, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := search.SearchParams{
				Query:    tt.query,
				Limit:    50,
				Fuzzy:    tt.fuzzy,
				Prefix:   tt.prefix,
				GeoMode:  "geopoint-centroid",
				Langs:    conf.Languages,
				Analyzer: conf.NameAnalyzer,
			}

			results, err := search.Search(idx, params)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			count := int(results.Total)
			if count < tt.expectMin || count > tt.expectMax {
				t.Errorf("expected %d-%d results, got %d", tt.expectMin, tt.expectMax, count)
			}
		})
	}
}

// TestAnalyzer_EdgeNgram tests the edge_ngram analyzer for autocomplete.
func TestAnalyzer_EdgeNgram(t *testing.T) {
	pbfPath := downloadPBF(t)
	conf := defaultTestConfig()
	conf.NameAnalyzer = "edge_ngram"

	_, idx := buildTestIndex(t, pbfPath, conf)
	defer idx.Close()

	tests := []struct {
		name      string
		query     string
		expectMin int
	}{
		{"full word: Vaduz", "Vaduz", 1},
		{"prefix: vad", "vad", 1},
		{"prefix: v", "v", 1},
		{"prefix: sch", "sch", 1},
		{"partial: vadu", "vadu", 1},
		{"autocomplete: rest", "rest", 0}, // May or may not match
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := search.SearchParams{
				Query:    tt.query,
				Limit:    50,
				GeoMode:  "geopoint-centroid",
				Langs:    conf.Languages,
				Analyzer: conf.NameAnalyzer,
			}

			results, err := search.Search(idx, params)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			count := int(results.Total)
			if count < tt.expectMin {
				t.Errorf("expected at least %d results, got %d", tt.expectMin, count)
			}
		})
	}
}

// TestAnalyzer_Ngram tests the ngram analyzer for substring matching.
func TestAnalyzer_Ngram(t *testing.T) {
	pbfPath := downloadPBF(t)
	conf := defaultTestConfig()
	conf.NameAnalyzer = "ngram"

	_, idx := buildTestIndex(t, pbfPath, conf)
	defer idx.Close()

	tests := []struct {
		name      string
		query     string
		expectMin int
	}{
		{"full word: Vaduz", "Vaduz", 1},
		{"substring: adu", "adu", 1}, // Should match "Vaduz" via substring
		{"substring: cha", "cha", 1}, // Should match "Schaan" via substring
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := search.SearchParams{
				Query:    tt.query,
				Limit:    50,
				GeoMode:  "geopoint-centroid",
				Langs:    conf.Languages,
				Analyzer: conf.NameAnalyzer,
			}

			results, err := search.Search(idx, params)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			count := int(results.Total)
			if count < tt.expectMin {
				t.Errorf("expected at least %d results, got %d", tt.expectMin, count)
			}
		})
	}
}

// TestAnalyzer_Keyword tests the keyword analyzer for exact matching.
func TestAnalyzer_Keyword(t *testing.T) {
	pbfPath := downloadPBF(t)
	conf := defaultTestConfig()
	conf.NameAnalyzer = "keyword"

	_, idx := buildTestIndex(t, pbfPath, conf)
	defer idx.Close()

	tests := []struct {
		name      string
		query     string
		expectMin int
		expectMax int
	}{
		{"exact: Vaduz", "Vaduz", 1, 100},
		{"partial: vad", "vad", 0, 0},        // Should NOT match with keyword analyzer
		{"wrong case: vaduz", "vaduz", 0, 0}, // Case-sensitive with keyword
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := search.SearchParams{
				Query:    tt.query,
				Limit:    50,
				GeoMode:  "geopoint-centroid",
				Langs:    conf.Languages,
				Analyzer: conf.NameAnalyzer,
			}

			results, err := search.Search(idx, params)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			count := int(results.Total)
			if count < tt.expectMin || count > tt.expectMax {
				t.Errorf("expected %d-%d results, got %d", tt.expectMin, tt.expectMax, count)
			}
		})
	}
}

// TestSearch_GeoFilter tests geo-filtered search.
func TestSearch_GeoFilter(t *testing.T) {
	pbfPath := downloadPBF(t)
	conf := defaultTestConfig()
	conf.NameAnalyzer = "standard"

	_, idx := buildTestIndex(t, pbfPath, conf)
	defer idx.Close()

	// Vaduz coordinates
	lat := 47.14
	lon := 9.52

	tests := []struct {
		name      string
		radius    string
		expectMin int
	}{
		{"100m radius", "100m", 0},
		{"1km radius", "1km", 0},
		{"5km radius", "5km", 1},
		{"50km radius", "50km", 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := search.SearchParams{
				Query:    "Vaduz",
				Lat:      &lat,
				Lon:      &lon,
				Radius:   tt.radius,
				Limit:    50,
				GeoMode:  "geopoint-centroid",
				Langs:    conf.Languages,
				Analyzer: conf.NameAnalyzer,
			}

			results, err := search.Search(idx, params)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			count := int(results.Total)
			if count < tt.expectMin {
				t.Errorf("expected at least %d results, got %d", tt.expectMin, count)
			}
		})
	}
}

// TestSearch_ClassFilter tests filtering by class.
func TestSearch_ClassFilter(t *testing.T) {
	pbfPath := downloadPBF(t)
	conf := defaultTestConfig()
	conf.NameAnalyzer = "standard"

	_, idx := buildTestIndex(t, pbfPath, conf)
	defer idx.Close()

	tests := []struct {
		name      string
		class     string
		expectMin int
	}{
		{"place class", "place", 1},
		{"amenity class", "amenity", 0}, // May have results
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := search.SearchParams{
				Query:    "",
				Class:    tt.class,
				Limit:    50,
				GeoMode:  "geopoint-centroid",
				Langs:    conf.Languages,
				Analyzer: conf.NameAnalyzer,
			}

			results, err := search.Search(idx, params)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			count := int(results.Total)
			if count < tt.expectMin {
				t.Errorf("expected at least %d results, got %d", tt.expectMin, count)
			}
		})
	}
}

// TestSearch_EmptyQuery tests searching with no query (returns all).
func TestSearch_EmptyQuery(t *testing.T) {
	pbfPath := downloadPBF(t)
	conf := defaultTestConfig()
	conf.NameAnalyzer = "standard"

	_, idx := buildTestIndex(t, pbfPath, conf)
	defer idx.Close()

	params := search.SearchParams{
		Query:    "",
		Limit:    10,
		GeoMode:  "geopoint-centroid",
		Langs:    conf.Languages,
		Analyzer: conf.NameAnalyzer,
	}

	results, err := search.Search(idx, params)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	count := int(results.Total)
	if count < 1 {
		t.Errorf("expected at least 1 result, got %d", count)
	}
}

// TestSearch_MultiLanguage tests searching across multiple language fields.
func TestSearch_MultiLanguage(t *testing.T) {
	pbfPath := downloadPBF(t)
	conf := defaultTestConfig()
	conf.NameAnalyzer = "standard"
	conf.Languages = []string{"en", "de", "fr"}

	_, idx := buildTestIndex(t, pbfPath, conf)
	defer idx.Close()

	params := search.SearchParams{
		Query:    "Vaduz",
		Limit:    50,
		GeoMode:  "geopoint-centroid",
		Langs:    conf.Languages,
		Analyzer: conf.NameAnalyzer,
	}

	results, err := search.Search(idx, params)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	count := int(results.Total)
	if count < 1 {
		t.Errorf("expected at least 1 result, got %d", count)
	}
}

// TestIndex_Reopen tests that an index can be reopened correctly.
func TestIndex_Reopen(t *testing.T) {
	pbfPath := downloadPBF(t)
	conf := defaultTestConfig()
	conf.NameAnalyzer = "standard"

	indexPath, idx1 := buildTestIndex(t, pbfPath, conf)
	idx1.Close()

	// Reopen the same index
	idx2, err := search.OpenOrCreateIndex(indexPath, search.BuildIndexMapping(conf))
	if err != nil {
		t.Fatalf("failed to reopen index: %v", err)
	}
	defer idx2.Close()

	// Verify we can search it
	params := search.SearchParams{
		Query:    "Vaduz",
		Limit:    10,
		GeoMode:  "geopoint-centroid",
		Langs:    conf.Languages,
		Analyzer: conf.NameAnalyzer,
	}

	results, err := search.Search(idx2, params)
	if err != nil {
		t.Fatalf("search on reopened index failed: %v", err)
	}

	if int(results.Total) < 1 {
		t.Errorf("expected at least 1 result, got %d", int(results.Total))
	}
}

// BenchmarkSearch compares search latency across different analyzer types.
func BenchmarkSearch(b *testing.B) {
	// Use a smaller test file or skip if PBF not available
	pbfPath := filepath.Join(testDataDir, DefaultPBF)
	if _, err := os.Stat(pbfPath); err != nil {
		b.Skip("PBF file not available, run tests first to download")
	}

	analyzers := []string{"standard", "edge_ngram", "ngram", "keyword"}

	for _, analyzer := range analyzers {
		b.Run(analyzer, func(b *testing.B) {
			conf := defaultTestConfig()
			conf.NameAnalyzer = analyzer

			_, idx := buildTestIndexForBenchmark(b, pbfPath, conf)
			defer idx.Close()

			b.ResetTimer()
			for range b.N {
				params := search.SearchParams{
					Query:    "Vaduz",
					Limit:    50,
					GeoMode:  "geopoint-centroid",
					Langs:    conf.Languages,
					Analyzer: conf.NameAnalyzer,
				}
				_, err := search.Search(idx, params)
				if err != nil {
					b.Fatalf("search failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkNewFeatures benchmarks the new features to verify no performance regression.
func BenchmarkNewFeatures(b *testing.B) {
	pbfPath := filepath.Join(testDataDir, DefaultPBF)
	if _, err := os.Stat(pbfPath); err != nil {
		b.Skip("PBF file not available, run tests first to download")
	}

	conf := defaultTestConfig()
	conf.NameAnalyzer = "standard"

	_, idx := buildTestIndexForBenchmark(b, pbfPath, conf)
	defer idx.Close()

	b.Run("Normalization", func(b *testing.B) {
		// Test normalization overhead
		b.ResetTimer()
		for range b.N {
			params := search.SearchParams{
				Query:    "München",
				Limit:    50,
				GeoMode:  "geopoint-centroid",
				Langs:    conf.Languages,
				Analyzer: conf.NameAnalyzer,
			}
			_, err := search.Search(idx, params)
			if err != nil {
				b.Fatalf("search failed: %v", err)
			}
		}
	})

	b.Run("NearQuery", func(b *testing.B) {
		// Test NearSearch overhead
		b.ResetTimer()
		for range b.N {
			params := search.SearchParams{
				Query:    "restaurant near Vaduz",
				Limit:    50,
				GeoMode:  "geopoint-centroid",
				Langs:    conf.Languages,
				Analyzer: conf.NameAnalyzer,
			}
			_, err := search.Search(idx, params)
			if err != nil {
				b.Fatalf("search failed: %v", err)
			}
		}
	})

	b.Run("MultiInterpretation", func(b *testing.B) {
		// Test multi-interpretation overhead
		b.ResetTimer()
		for range b.N {
			params := search.SearchParams{
				Query:    "Vaduz center",
				Limit:    50,
				GeoMode:  "geopoint-centroid",
				Langs:    conf.Languages,
				Analyzer: conf.NameAnalyzer,
			}
			_, err := search.Search(idx, params)
			if err != nil {
				b.Fatalf("search failed: %v", err)
			}
		}
	})

	b.Run("FrequencyAware", func(b *testing.B) {
		// Test frequency-aware optimization
		b.ResetTimer()
		for range b.N {
			params := search.SearchParams{
				Query:    "the great restaurant",
				Limit:    50,
				GeoMode:  "geopoint-centroid",
				Langs:    conf.Languages,
				Analyzer: conf.NameAnalyzer,
			}
			_, err := search.Search(idx, params)
			if err != nil {
				b.Fatalf("search failed: %v", err)
			}
		}
	})

	b.Run("Baseline_Simple", func(b *testing.B) {
		// Baseline: simple single-word query
		b.ResetTimer()
		for range b.N {
			params := search.SearchParams{
				Query:    "Vaduz",
				Limit:    50,
				GeoMode:  "geopoint-centroid",
				Langs:    conf.Languages,
				Analyzer: conf.NameAnalyzer,
			}
			_, err := search.Search(idx, params)
			if err != nil {
				b.Fatalf("search failed: %v", err)
			}
		}
	})
}

// buildTestIndexForBenchmark is like buildTestIndex but for benchmarks.
func buildTestIndexForBenchmark(b *testing.B, pbfPath string, conf *config.Config) (string, bleve.Index) {
	b.Helper()

	tempDir := b.TempDir()
	indexPath := filepath.Join(tempDir, "test.bleve")

	conf.IndexPath = indexPath
	conf.GeometryMode = "geopoint-centroid"
	conf.StoreMetadata = false
	conf.StoreGeometry = false

	m := search.BuildIndexMapping(conf)

	idx, err := search.OpenOrCreateIndex(indexPath, m)
	if err != nil {
		b.Fatalf("failed to create index: %v", err)
	}

	err = osm.BuildIndex(pbfPath, conf, idx)
	if err != nil {
		idx.Close()
		b.Fatalf("failed to build index: %v", err)
	}

	return indexPath, idx
}

// TestAnalyzer_IndexSizeComparison compares index sizes across analyzers.
func TestAnalyzer_IndexSizeComparison(t *testing.T) {
	pbfPath := downloadPBF(t)

	analyzers := []string{"standard", "edge_ngram", "ngram", "keyword"}

	type SizeResult struct {
		Analyzer string
		Size     int64
	}

	results := make([]SizeResult, 0, len(analyzers))

	for _, analyzer := range analyzers {
		conf := defaultTestConfig()
		conf.NameAnalyzer = analyzer

		indexPath, idx := buildTestIndex(t, pbfPath, conf)
		idx.Close()

		// Calculate directory size
		size, err := getDirSize(indexPath)
		if err != nil {
			t.Fatalf("failed to get directory size: %v", err)
		}

		results = append(results, SizeResult{
			Analyzer: analyzer,
			Size:     size,
		})
	}

	// Print comparison table
	t.Logf("\nIndex Size Comparison:")
	t.Logf("%-18s %-15s", "Analyzer", "Size")
	t.Logf("----------------------------------------")
	for _, r := range results {
		t.Logf("%-18s %-15s", r.Analyzer, formatSize(r.Size))
	}
}

// getDirSize calculates the total size of a directory.
func getDirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// formatSize formats bytes into a human-readable string.
func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// TestWikimediaImportance_Integration tests loading and using wikimedia importance.
func TestWikimediaImportance_Integration(t *testing.T) {
	path := loadWikidataImportance(t)
	if path == "" {
		t.Skip("wikimedia importance file not available")
	}

	lookup, err := osm.LoadWikidataImportance(path)
	if err != nil {
		t.Fatalf("failed to load wikidata importance: %v", err)
	}

	t.Logf("loaded %d wikidata importance scores", lookup.Size())

	if lookup.Size() < 1000 {
		t.Errorf("expected at least 1000 entries, got %d", lookup.Size())
	}
}

// TestDownload_GzipFormat tests downloading a small gzip file.
func TestDownload_GzipFormat(t *testing.T) {
	// Create a small test gz file to verify decompression works
	tmpDir := t.TempDir()
	gzPath := filepath.Join(tmpDir, "test.csv.gz")

	// Create gzipped content
	f, err := os.Create(gzPath)
	if err != nil {
		t.Fatal(err)
	}

	gzWriter := gzip.NewWriter(f)
	_, err = gzWriter.Write([]byte("test,data\nhello,world\n"))
	if err != nil {
		t.Fatal(err)
	}
	gzWriter.Close()
	f.Close()

	// Verify we can read it
	f2, err := os.Open(gzPath)
	if err != nil {
		t.Fatalf("failed to open gz file: %v", err)
	}
	defer f2.Close()

	gzReader, err := gzip.NewReader(f2)
	if err != nil {
		t.Fatalf("failed to create gz reader: %v", err)
	}
	defer gzReader.Close()

	content, err := io.ReadAll(gzReader)
	if err != nil {
		t.Fatalf("failed to read gz file: %v", err)
	}

	if string(content) != "test,data\nhello,world\n" {
		t.Errorf("unexpected content: %s", string(content))
	}
}

package tests_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

// BenchmarkBuildIndex benchmarks index building.
// Run with: go test -bench=BenchmarkBuildIndex -benchmem ./tests/
// Each run builds a fresh index and reports timing + doc count.
func BenchmarkBuildIndex(b *testing.B) {
	pbfPath := "../liechtenstein-latest.osm.pbf"
	if _, err := os.Stat(pbfPath); os.IsNotExist(err) {
		b.Fatalf("test PBF file not found at %s. Run scripts/update.sh to download it.", pbfPath)
	}

	tmpDir := b.TempDir()
	indexPath := filepath.Join(tmpDir, "test.bleve")

	conf := &config.Config{
		IndexPaths:   []string{indexPath},
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
		NameAnalyzer: "standard",
	}

	mapping := search.BuildIndexMapping(conf)
	index, err := search.OpenOrCreateIndex(indexPath, mapping)
	if err != nil {
		b.Fatalf("failed to create index: %v", err)
	}
	defer index.Close()

	start := time.Now()

	err = osm.BuildIndex(pbfPath, conf, index)
	if err != nil {
		b.Fatalf("BuildIndex failed: %v", err)
	}

	elapsed := time.Since(start)

	docCount, err := index.DocCount()
	if err != nil {
		b.Fatalf("failed to get doc count: %v", err)
	}

	b.ReportMetric(float64(elapsed.Milliseconds()), "ms_total")
	b.ReportMetric(float64(docCount), "docs")
}

// TestBuildIndex_Search verifies that indexed documents are searchable.
func TestBuildIndex_Search(t *testing.T) {
	// Skip if test PBF doesn't exist
	pbfPath := "../liechtenstein-latest.osm.pbf"
	if _, err := os.Stat(pbfPath); os.IsNotExist(err) {
		t.Fatalf("test PBF file not found at %s. Run scripts/update.sh to download it.", pbfPath)
	}

	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test.bleve")

	conf := &config.Config{
		IndexPaths:   []string{indexPath},
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
		NameAnalyzer: "standard",
	}

	mapping := search.BuildIndexMapping(conf)
	index, err := search.OpenOrCreateIndex(indexPath, mapping)
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}
	defer index.Close()

	err = osm.BuildIndex(pbfPath, conf, index)
	if err != nil {
		t.Fatalf("BuildIndex failed: %v", err)
	}

	// Perform a search query
	params := search.SearchParams{
		Query: "Vaduz", // Capital of Liechtenstein
		Limit: 10,
		Langs: []string{"en"},
	}

	results, err := search.Search(index, params)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if results.Total == 0 {
		t.Error("expected results for 'Vaduz', got 0")
	}

	t.Logf("Search for 'Vaduz' returned %d results", results.Total)
}

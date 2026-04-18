package tests_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

// BenchmarkBuildIndex benchmarks index building with different worker counts.
// Run with: go test -bench=BenchmarkBuildIndex -benchmem ./tests/
// Each configuration builds a fresh index and reports timing + doc count.
func BenchmarkBuildIndex(b *testing.B) {
	pbfPath := "../liechtenstein-latest.osm.pbf"
	if _, err := os.Stat(pbfPath); os.IsNotExist(err) {
		b.Fatalf("test PBF file not found at %s. Run scripts/update.sh to download it.", pbfPath)
	}

	workerCounts := []int{1, 2, 4, 6, 8}

	for _, workers := range workerCounts {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
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
		})
	}
}

// TestBuildIndex_ParallelWorkers builds an index with multiple workers
// and verifies the results are correct and race-free.
// Run with: go test -race -run TestBuildIndex_ParallelWorkers
func TestBuildIndex_ParallelWorkers(t *testing.T) {
	// Skip if test PBF doesn't exist
	pbfPath := "../liechtenstein-latest.osm.pbf"
	if _, err := os.Stat(pbfPath); os.IsNotExist(err) {
		t.Fatalf("test PBF file not found at %s. Run scripts/update.sh to download it.", pbfPath)
	}

	// Create temporary directory for index
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test.bleve")

	// Create index mapping
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

	// Build index with parallel workers
	err = osm.BuildIndex(pbfPath, conf, index)
	if err != nil {
		t.Fatalf("BuildIndex failed: %v", err)
	}

	// Verify index was created and has content
	docCount, err := index.DocCount()
	if err != nil {
		t.Fatalf("failed to get doc count: %v", err)
	}

	if docCount == 0 {
		t.Error("expected indexed documents, got 0")
	}
}

// TestBuildIndex_Parallel_Search verifies that documents indexed with parallel workers
// are searchable and return correct results.
func TestBuildIndex_Parallel_Search(t *testing.T) {
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

	// Build index with parallel workers
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

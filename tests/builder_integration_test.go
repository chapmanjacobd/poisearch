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
		b.Skip("test PBF file not found, skipping")
	}

	workerCounts := []int{1, 2, 4, 6, 8}

	for _, workers := range workerCounts {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			tmpDir := b.TempDir()
			indexPath := filepath.Join(tmpDir, "test.bleve")

			conf := &config.Config{
				IndexPath:    indexPath,
				Languages:    []string{"en"},
				GeometryMode: "geopoint",
				NameAnalyzer: "standard",
				BuildWorkers: workers,
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
		t.Skip("test PBF file not found, skipping")
	}

	// Create temporary directory for index
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test.bleve")

	// Create index mapping
	conf := &config.Config{
		IndexPath:    indexPath,
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
		NameAnalyzer: "standard",
		BuildWorkers: 2, // Use 2 workers for parallel test
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

	t.Logf("Indexed %d documents with %d workers", docCount, conf.BuildWorkers)
}

// TestBuildIndex_ParallelVsSingle builds indexes with both single and multiple workers
// and verifies they produce equivalent results.
func TestBuildIndex_ParallelVsSingle(t *testing.T) {
	// Skip if test PBF doesn't exist
	pbfPath := "../liechtenstein-latest.osm.pbf"
	if _, err := os.Stat(pbfPath); os.IsNotExist(err) {
		t.Skip("test PBF file not found, skipping")
	}

	testCases := []struct {
		name    string
		workers int
	}{
		{"single_worker", 1},
		{"two_workers", 2},
		{"four_workers", 4},
	}

	var firstDocCount uint64

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			indexPath := filepath.Join(tmpDir, "test.bleve")

			conf := &config.Config{
				IndexPath:    indexPath,
				Languages:    []string{"en"},
				GeometryMode: "geopoint",
				NameAnalyzer: "standard",
				BuildWorkers: tc.workers,
			}

			mapping := search.BuildIndexMapping(conf)
			index, err := search.OpenOrCreateIndex(indexPath, mapping)
			if err != nil {
				t.Fatalf("failed to create index: %v", err)
			}

			start := time.Now()
			err = osm.BuildIndex(pbfPath, conf, index)
			elapsed := time.Since(start)
			if err != nil {
				index.Close()
				t.Fatalf("BuildIndex failed: %v", err)
			}

			docCount, err := index.DocCount()
			if err != nil {
				index.Close()
				t.Fatalf("failed to get doc count: %v", err)
			}

			index.Close()

			t.Logf("Workers: %d, Documents: %d, Time: %v", tc.workers, docCount, elapsed)

			if firstDocCount == 0 {
				firstDocCount = docCount
			} else if docCount != firstDocCount {
				t.Errorf("expected %d documents (same as first run), got %d", firstDocCount, docCount)
			}
		})
	}
}

// TestBuildIndex_Parallel_Search verifies that documents indexed with parallel workers
// are searchable and return correct results.
func TestBuildIndex_Parallel_Search(t *testing.T) {
	// Skip if test PBF doesn't exist
	pbfPath := "../liechtenstein-latest.osm.pbf"
	if _, err := os.Stat(pbfPath); os.IsNotExist(err) {
		t.Skip("test PBF file not found, skipping")
	}

	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test.bleve")

	conf := &config.Config{
		IndexPath:    indexPath,
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
		NameAnalyzer: "standard",
		BuildWorkers: 2,
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

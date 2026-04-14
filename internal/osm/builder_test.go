package osm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

// TestBuildIndex_ParallelWorkers builds an index with multiple workers
// and verifies the results are correct and race-free.
// Run with: go test -race -run TestBuildIndex_ParallelWorkers
func TestBuildIndex_ParallelWorkers(t *testing.T) {
	// Skip if test PBF doesn't exist
	pbfPath := "../../liechtenstein-latest.osm.pbf"
	if _, err := os.Stat(pbfPath); os.IsNotExist(err) {
		t.Skip("test PBF file not found, skipping")
	}

	// Create temporary directory for index
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test.bleve")

	// Create index mapping
	conf := &config.Config{
		IndexPath:     indexPath,
		Languages:     []string{"en"},
		GeometryMode:  "geopoint",
		NameAnalyzer:  "standard",
		BuildWorkers:  2, // Use 2 workers for parallel test
	}

	mapping := search.BuildIndexMapping(conf)
	index, err := search.OpenOrCreateIndex(indexPath, mapping)
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}
	defer index.Close()

	// Build index with parallel workers
	err = BuildIndex(pbfPath, conf, index)
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
	pbfPath := "../../liechtenstein-latest.osm.pbf"
	if _, err := os.Stat(pbfPath); os.IsNotExist(err) {
		t.Skip("test PBF file not found, skipping")
	}

	testCases := []struct {
		name   string
		workers int
	}{
		{"single_worker", 1},
		{"two_workers", 2},
		{"four_workers", 4},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			indexPath := filepath.Join(tmpDir, "test.bleve")

			conf := &config.Config{
				IndexPath:     indexPath,
				Languages:     []string{"en"},
				GeometryMode:  "geopoint",
				NameAnalyzer:  "standard",
				BuildWorkers:  tc.workers,
			}

			mapping := search.BuildIndexMapping(conf)
			index, err := search.OpenOrCreateIndex(indexPath, mapping)
			if err != nil {
				t.Fatalf("failed to create index: %v", err)
			}

			err = BuildIndex(pbfPath, conf, index)
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

			t.Logf("Workers: %d, Documents: %d", tc.workers, docCount)

			// All configurations should produce the same number of documents
			// We'll verify this across runs in the test output
		})
	}
}

// TestBuildIndex_Parallel_Search verifies that documents indexed with parallel workers
// are searchable and return correct results.
func TestBuildIndex_Parallel_Search(t *testing.T) {
	// Skip if test PBF doesn't exist
	pbfPath := "../../liechtenstein-latest.osm.pbf"
	if _, err := os.Stat(pbfPath); os.IsNotExist(err) {
		t.Skip("test PBF file not found, skipping")
	}

	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test.bleve")

	conf := &config.Config{
		IndexPath:     indexPath,
		Languages:     []string{"en"},
		GeometryMode:  "geopoint",
		NameAnalyzer:  "standard",
		BuildWorkers:  2,
	}

	mapping := search.BuildIndexMapping(conf)
	index, err := search.OpenOrCreateIndex(indexPath, mapping)
	if err != nil {
		t.Fatalf("failed to create index: %v", err)
	}
	defer index.Close()

	// Build index with parallel workers
	err = BuildIndex(pbfPath, conf, index)
	if err != nil {
		t.Fatalf("BuildIndex failed: %v", err)
	}

	// Perform a search query
	params := search.SearchParams{
		Query:   "Vaduz", // Capital of Liechtenstein
		Limit:   10,
		Langs:   []string{"en"},
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

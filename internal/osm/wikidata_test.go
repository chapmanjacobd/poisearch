package osm_test

import (
	"compress/gzip"
	"os"
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/osm"
)

func TestLoadWikidataImportance(t *testing.T) {
	// Try to load the user's actual wikimedia file (in project root)
	path := "../../wikimedia-importance-2025-11.csv.gz"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("wikimedia file not found at %s, skipping integration test", path)
	}

	lookup, err := osm.LoadWikidataImportance(path)
	if err != nil {
		t.Fatalf("failed to load wikidata importance: %v", err)
	}

	t.Logf("loaded %d QID importance scores", lookup.Size())

	// Verify we can look up some known QIDs
	// Q30 = United States (should be importance 1.0)
	usImportance := lookup.GetImportance("Q30")
	t.Logf("Q30 (United States) importance: %f", usImportance)

	// Q371 = !!! (should have some importance)
	q371Importance := lookup.GetImportance("Q371")
	t.Logf("Q371 (!!!) importance: %f", q371Importance)

	// Unknown QID should return 0
	unknownImportance := lookup.GetImportance("Q999999999")
	if unknownImportance != 0 {
		t.Errorf("expected 0 for unknown QID, got %f", unknownImportance)
	}
}

func TestLoadWikidataImportanceSample(t *testing.T) {
	// Create a small test file with the format from the user's data
	tmpFile, err := os.CreateTemp(t.TempDir(), "wikidata-test-*.csv.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	// Write test data in gzip format
	gzWriter := gzip.NewWriter(tmpFile)
	testData := `language	type	title	importance	wikidata_id
en	a	!!!	0.41523351747267634	Q371
en	a	01099	0.2869812649426343	Q106604851
en	a	01_Distribution	0.45833613469028417	Q1554656
en	a	07th_Expansion	0.39416088441914005	Q161223
de	a	Berlin	0.85	Berlin_Q64
`
	gzWriter.Write([]byte(testData))
	gzWriter.Close()
	tmpFile.Close()

	// Load the test file
	lookup, err := osm.LoadWikidataImportance(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to load test wikidata importance: %v", err)
	}

	if lookup.Size() != 4 {
		t.Errorf("expected 4 entries, got %d", lookup.Size())
	}

	// Verify specific entries
	tests := []struct {
		qid       string
		expect    float64
		tolerance float64
	}{
		{"Q371", 0.41523351747267634, 0.0001},
		{"Q106604851", 0.2869812649426343, 0.0001},
		{"Q1554656", 0.45833613469028417, 0.0001},
		{"Q161223", 0.39416088441914005, 0.0001},
	}

	for _, tt := range tests {
		got := lookup.GetImportance(tt.qid)
		if got == 0 {
			t.Errorf("QID %s not found", tt.qid)
			continue
		}
		diff := got - tt.expect
		if diff < 0 {
			diff = -diff
		}
		if diff > tt.tolerance {
			t.Errorf("QID %s: expected importance %f, got %f", tt.qid, tt.expect, got)
		}
	}

	// Verify header was skipped (Berlin_Q64 is not a valid QID)
	if lookup.GetImportance("Berlin_Q64") != 0 {
		t.Error("header line should have been skipped")
	}
}

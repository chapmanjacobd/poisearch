package tests_test

import (
	"os"
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/osm"
)

func TestLoadWikidataImportance(t *testing.T) {
	// Try to load the user's actual wikimedia file (in project root)
	path := "../wikimedia-importance-2025-11.csv.gz"
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("wikimedia importance file not found at %s. Integration test requires this file.", path)
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

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

func TestLoadWikidataImportanceWithRedirects(t *testing.T) {
	// Create a test file with both articles and redirects
	tmpFile, err := os.CreateTemp(t.TempDir(), "wikidata-redirects-test-*.tsv.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	// Write test data in gzip format (TSV format from wikipedia-wikidata pipeline)
	gzWriter := gzip.NewWriter(tmpFile)
	testData := `language	type	title	importance	wikidata_id
en	a	Brandenburg_Gate	0.5531125195487524	Q82425
en	r	Berlin's_Gate	0.5531125195487524	Q82425
en	r	Brandenberg_Gate	0.5531125195487524	Q82425
en	r	Brandenburger_Tor	0.5531125195487524	Q82425
en	a	Eiffel_Tower	0.7	Q243
fr	a	Tour_Eiffel	0.7	Q243
fr	r	Tour_de_Paris	0.7	Q243
`
	gzWriter.Write([]byte(testData))
	gzWriter.Close()
	tmpFile.Close()

	lookup, err := osm.LoadWikidataImportance(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to load test wikidata importance: %v", err)
	}

	// Verify size (only articles, not redirects)
	if lookup.Size() != 2 {
		t.Errorf("expected 2 QIDs, got %d", lookup.Size())
	}

	// Test redirect lookup
	redirects := lookup.GetRedirects("Q82425")
	if len(redirects) != 3 {
		t.Errorf("expected 3 redirects for Q82425, got %d: %v", len(redirects), redirects)
	}

	// Verify specific redirects
	expectedRedirects := map[string]bool{
		"Berlin's_Gate":     true,
		"Brandenberg_Gate":  true,
		"Brandenburger_Tor": true,
	}

	for _, r := range redirects {
		if !expectedRedirects[r] {
			t.Errorf("unexpected redirect for Q82425: %s", r)
		}
	}

	// Test Q243 redirects (only 1 redirect from French)
	eiffelRedirects := lookup.GetRedirects("Q243")
	if len(eiffelRedirects) != 1 {
		t.Errorf("expected 1 redirect for Q243, got %d", len(eiffelRedirects))
	}
	if len(eiffelRedirects) > 0 && eiffelRedirects[0] != "Tour_de_Paris" {
		t.Errorf("expected redirect Tour_de_Paris, got %s", eiffelRedirects[0])
	}

	// Test QID with no redirects
	noRedirects := lookup.GetRedirects("Q999999")
	if noRedirects != nil {
		t.Errorf("expected nil for QID with no redirects, got %v", noRedirects)
	}

	// Verify importance still works
	brandenburgImportance := lookup.GetImportance("Q82425")
	if brandenburgImportance == 0 {
		t.Error("expected non-zero importance for Q82425")
	}

	// Test language-specific lookup for Q243 (should prefer French score)
	frImportance := lookup.GetImportanceForLang("Q243", "fr")
	if frImportance == 0 {
		t.Error("expected non-zero French importance for Q243")
	}
}

func TestRedirectCount(t *testing.T) {
	// Create a test file with redirects
	tmpFile, err := os.CreateTemp(t.TempDir(), "wikidata-redirect-count-*.tsv.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	gzWriter := gzip.NewWriter(tmpFile)
	testData := `language	type	title	importance	wikidata_id
en	a	Test_Article_1	0.5	Q1
en	r	Redirect_1a	0.5	Q1
en	r	Redirect_1b	0.5	Q1
en	a	Test_Article_2	0.6	Q2
en	r	Redirect_2a	0.6	Q2
en	r	Redirect_2b	0.6	Q2
en	r	Redirect_2c	0.6	Q2
en	a	Test_Article_3	0.7	Q3
`
	gzWriter.Write([]byte(testData))
	gzWriter.Close()
	tmpFile.Close()

	lookup, err := osm.LoadWikidataImportance(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to load test wikidata importance: %v", err)
	}

	// Total redirect count should be 5 (2 for Q1 + 3 for Q2)
	totalRedirects := lookup.RedirectCount()
	if totalRedirects != 5 {
		t.Errorf("expected 5 total redirects, got %d", totalRedirects)
	}

	// Size should be 3 (Q1, Q2, Q3)
	if lookup.Size() != 3 {
		t.Errorf("expected 3 QIDs, got %d", lookup.Size())
	}
}

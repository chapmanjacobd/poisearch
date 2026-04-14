package osm_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/osm"
)

func TestDefaultOntology(t *testing.T) {
	ont := osm.DefaultOntology()

	if ont == nil {
		t.Fatal("DefaultOntology() returned nil")
	}

	tests := []struct {
		name      string
		qid       string
		wantLevel int
		wantLabel string
	}{
		{"country", "Q6256", 0, "country"},
		{"city", "Q515", 2, "city"},
		{"village", "Q532", 3, "village"},
		{"restaurant", "Q11707", 3, "restaurant"},
		{"hotel", "Q40231", 3, "hotel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLevel := ont.GetLevel(tt.qid)
			if gotLevel != tt.wantLevel {
				t.Errorf("GetLevel(%s) = %d, want %d", tt.qid, gotLevel, tt.wantLevel)
			}

			gotLabel := ont.GetLabel(tt.qid)
			if gotLabel != tt.wantLabel {
				t.Errorf("GetLabel(%s) = %s, want %s", tt.qid, gotLabel, tt.wantLabel)
			}
		})
	}
}

func TestOntology_GetQIDsForOSM(t *testing.T) {
	ont := osm.DefaultOntology()

	tests := []struct {
		name    string
		osmKey  string
		osmVal  string
		wantLen int
	}{
		{"place=country", "place", "country", 1},
		{"place=city", "place", "city", 1},
		{"building=yes", "building", "yes", 1},
		{"non-existent", "nonexistent", "value", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ont.GetQIDsForOSM(tt.osmKey, tt.osmVal)
			if len(got) != tt.wantLen {
				t.Errorf("GetQIDsForOSM(%s=%s) returned %d QIDs, want %d", tt.osmKey, tt.osmVal, len(got), tt.wantLen)
			}
		})
	}
}

func TestOntology_GetMinLevelForOSM(t *testing.T) {
	ont := osm.DefaultOntology()

	tests := []struct {
		name      string
		osmKey    string
		osmVal    string
		wantLevel int
	}{
		{"place=country", "place", "country", 0},
		{"place=city", "place", "city", 2},
		{"amenity=restaurant", "amenity", "restaurant", 3},
		{"non-existent", "nonexistent", "value", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ont.GetMinLevelForOSM(tt.osmKey, tt.osmVal)
			if got != tt.wantLevel {
				t.Errorf("GetMinLevelForOSM(%s=%s) = %d, want %d", tt.osmKey, tt.osmVal, got, tt.wantLevel)
			}
		})
	}
}

func TestOntology_BoostByOntology(t *testing.T) {
	ont := osm.DefaultOntology()

	tests := []struct {
		name  string
		level int
		want  float64
	}{
		{"level 0", 0, 1.0},
		{"level 3", 3, 1.3},
		{"level 6", 6, 1.6},
		{"invalid -1", -1, 1.0},
		{"invalid 7", 7, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ont.BoostByOntology(tt.level)
			if got != tt.want {
				t.Errorf("BoostByOntology(%d) = %f, want %f", tt.level, got, tt.want)
			}
		})
	}
}

func TestLoadOntologyFromCSV(t *testing.T) {
	// Create a temporary CSV file
	tmpDir := t.TempDir()
	csvPath := filepath.Join(tmpDir, "test_ontology.csv")

	csvContent := `# Test ontology
Q6256,0,country,place,country
Q515,2,city,place,city
Q532,3,village,place,village
`
	if err := os.WriteFile(csvPath, []byte(csvContent), 0o644); err != nil {
		t.Fatalf("failed to write test CSV: %v", err)
	}

	ont, err := osm.LoadOntologyFromCSV(csvPath)
	if err != nil {
		t.Fatalf("LoadOntologyFromCSV() failed: %v", err)
	}

	// Verify loaded data
	tests := []struct {
		name      string
		qid       string
		wantLevel int
		wantLabel string
	}{
		{"country", "Q6256", 0, "country"},
		{"city", "Q515", 2, "city"},
		{"village", "Q532", 3, "village"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLevel := ont.GetLevel(tt.qid)
			if gotLevel != tt.wantLevel {
				t.Errorf("GetLevel(%s) = %d, want %d", tt.qid, gotLevel, tt.wantLevel)
			}

			gotLabel := ont.GetLabel(tt.qid)
			if gotLabel != tt.wantLabel {
				t.Errorf("GetLabel(%s) = %s, want %s", tt.qid, gotLabel, tt.wantLabel)
			}
		})
	}
}

func TestLoadOntologyFromCSV_InvalidFile(t *testing.T) {
	_, err := osm.LoadOntologyFromCSV("/nonexistent/file.csv")
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

func TestLoadOntologyFromCSV_InvalidFormat(t *testing.T) {
	tmpDir := t.TempDir()
	csvPath := filepath.Join(tmpDir, "bad_ontology.csv")

	// Malformed CSV (missing fields)
	csvContent := `Q6256,0,country
`
	if err := os.WriteFile(csvPath, []byte(csvContent), 0o644); err != nil {
		t.Fatalf("failed to write test CSV: %v", err)
	}

	ont, err := osm.LoadOntologyFromCSV(csvPath)
	if err != nil {
		t.Fatalf("LoadOntologyFromCSV() failed: %v", err)
	}

	// Should have loaded but with no valid entries
	if ont.GetLevel("Q6256") != -1 {
		t.Error("expected no valid entries from malformed CSV")
	}
}

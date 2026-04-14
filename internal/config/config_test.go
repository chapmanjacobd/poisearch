package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/pelletier/go-toml/v2"
)

func TestConfig_ParseValid(t *testing.T) {
	tomlContent := `
index_path = "test.bleve"
pbf_path = "test.pbf"
languages = ["en", "de"]
geometry_mode = "geopoint"
simplification_tolerance = 0.0001
name_analyzer = "standard"

[server]
host = "127.0.0.1"
port = 9889

[importance]
default = 1.0
population_boost_weight = 0.2
capital_boost = 2.0
wikipedia_boost = 1.5
boosts = ["city", "town", "pharmacy"]
`

	var conf config.Config
	err := toml.Unmarshal([]byte(tomlContent), &conf)
	if err != nil {
		t.Fatalf("failed to parse valid config: %v", err)
	}

	// Verify parsed values
	if conf.IndexPath != "test.bleve" {
		t.Errorf("index_path = %s, want test.bleve", conf.IndexPath)
	}
	if conf.PBFPath != "test.pbf" {
		t.Errorf("pbf_path = %s, want test.pbf", conf.PBFPath)
	}
	if len(conf.Languages) != 2 {
		t.Errorf("languages count = %d, want 2", len(conf.Languages))
	}
	if conf.GeometryMode != "geopoint" {
		t.Errorf("geometry_mode = %s, want geopoint", conf.GeometryMode)
	}
	if conf.Server.Host != "127.0.0.1" {
		t.Errorf("server.host = %s, want 127.0.0.1", conf.Server.Host)
	}
	if conf.Server.Port != 9889 {
		t.Errorf("server.port = %d, want 9889", conf.Server.Port)
	}
	if conf.Importance.Default != 1.0 {
		t.Errorf("importance.default = %f, want 1.0", conf.Importance.Default)
	}
	if len(conf.Importance.Boosts) != 3 {
		t.Errorf("importance.boosts count = %d, want 3", len(conf.Importance.Boosts))
	}
}

func TestConfig_StoreAddress(t *testing.T) {
	tomlContent := `
index_path = "test.bleve"
languages = ["en"]
store_address = true

[server]
host = "0.0.0.0"
port = 9889

[importance]
default = 1.0
`

	var conf config.Config
	err := toml.Unmarshal([]byte(tomlContent), &conf)
	if err != nil {
		t.Fatalf("failed to parse config with store_address: %v", err)
	}

	if !conf.StoreAddress {
		t.Error("store_address should be true")
	}
}

func TestConfig_DefaultValues(t *testing.T) {
	tomlContent := `
index_path = "test.bleve"
languages = ["en"]

[server]
host = "0.0.0.0"
port = 9889

[importance]
default = 1.0
`

	var conf config.Config
	err := toml.Unmarshal([]byte(tomlContent), &conf)
	if err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Verify boolean defaults are false
	if conf.NodesOnly {
		t.Error("nodes_only should default to false")
	}
	if conf.DisableAltNames {
		t.Error("disable_alt_names should default to false")
	}
	if conf.DisableImportance {
		t.Error("disable_importance should default to false")
	}
	if conf.DisableClassSubtype {
		t.Error("disable_class_subtype should default to false")
	}
	if conf.OnlyNamed {
		t.Error("only_named should default to false")
	}
	if conf.StoreMetadata {
		t.Error("store_metadata should default to false")
	}
	if conf.StoreGeometry {
		t.Error("store_geometry should default to false")
	}
	if conf.StoreAddress {
		t.Error("store_address should default to false")
	}

	// Verify optional paths are empty
	if conf.WikidataImportance != "" {
		t.Error("wikidata_importance should default to empty")
	}
	if conf.OntologyPath != "" {
		t.Error("ontology_path should default to empty")
	}
}

func TestConfig_AllGeometryModes(t *testing.T) {
	modes := []string{
		"geopoint",
		"geoshape-bbox",
		"geoshape-simplified",
		"geoshape-full",
		"no-geo",
	}

	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			tomlContent := `
index_path = "test.bleve"
languages = ["en"]
geometry_mode = "` + mode + `"

[server]
host = "0.0.0.0"
port = 9889

[importance]
default = 1.0
`
			var conf config.Config
			err := toml.Unmarshal([]byte(tomlContent), &conf)
			if err != nil {
				t.Fatalf("failed to parse config with geometry_mode=%s: %v", mode, err)
			}
			if conf.GeometryMode != mode {
				t.Errorf("geometry_mode = %s, want %s", conf.GeometryMode, mode)
			}
		})
	}
}

func TestConfig_AllAnalyzers(t *testing.T) {
	analyzers := []string{
		"standard",
		"edge_ngram",
		"ngram",
		"keyword",
	}

	for _, analyzer := range analyzers {
		t.Run(analyzer, func(t *testing.T) {
			if analyzer == "keyword" {
				t.Skip("skipping keyword analyzer")
			}
			tomlContent := `
index_path = "test.bleve"
languages = ["en"]
name_analyzer = "` + analyzer + `"

[server]
host = "0.0.0.0"
port = 9889

[importance]
default = 1.0
`
			var conf config.Config
			err := toml.Unmarshal([]byte(tomlContent), &conf)
			if err != nil {
				t.Fatalf("failed to parse config with name_analyzer=%s: %v", analyzer, err)
			}
			if conf.NameAnalyzer != analyzer {
				t.Errorf("name_analyzer = %s, want %s", conf.NameAnalyzer, analyzer)
			}
		})
	}
}

func TestConfig_FromFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	tomlContent := `
index_path = "test.bleve"
languages = ["en", "zh", "es"]
geometry_mode = "geopoint"
store_address = true
store_metadata = true

[server]
host = "localhost"
port = 8080

[importance]
default = 1.0
population_boost_weight = 0.3
boosts = ["city"]
`
	if err := os.WriteFile(configPath, []byte(tomlContent), 0o644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Read and parse
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}

	var conf config.Config
	if err := toml.Unmarshal(data, &conf); err != nil {
		t.Fatalf("failed to parse config from file: %v", err)
	}

	// Verify
	if conf.IndexPath != "test.bleve" {
		t.Errorf("index_path = %s, want test.bleve", conf.IndexPath)
	}
	if len(conf.Languages) != 3 {
		t.Errorf("expected 3 languages, got %d", len(conf.Languages))
	}
	if !conf.StoreAddress {
		t.Error("store_address should be true")
	}
	if !conf.StoreMetadata {
		t.Error("store_metadata should be true")
	}
	if conf.Server.Port != 8080 {
		t.Errorf("server.port = %d, want 8080", conf.Server.Port)
	}
	if len(conf.Importance.Boosts) != 1 || conf.Importance.Boosts[0] != "city" {
		t.Errorf("expected boost ['city'], got %v", conf.Importance.Boosts)
	}
}

func TestConfig_ImportanceWeights(t *testing.T) {
	tomlContent := `
index_path = "test.bleve"
languages = ["en"]

[server]
host = "0.0.0.0"
port = 9889

[importance]
default = 2.0
population_boost_weight = 0.5
capital_boost = 3.0
wikipedia_boost = 2.5
boosts = ["city", "town", "hospital"]
`

	var conf config.Config
	err := toml.Unmarshal([]byte(tomlContent), &conf)
	if err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	// Verify importance weights
	if conf.Importance.Default != 2.0 {
		t.Errorf("importance.default = %f, want 2.0", conf.Importance.Default)
	}
	if conf.Importance.PopBoost != 0.5 {
		t.Errorf("importance.population_boost_weight = %f, want 0.5", conf.Importance.PopBoost)
	}
	if conf.Importance.Capital != 3.0 {
		t.Errorf("importance.capital_boost = %f, want 3.0", conf.Importance.Capital)
	}
	if conf.Importance.Wiki != 2.5 {
		t.Errorf("importance.wikipedia_boost = %f, want 2.5", conf.Importance.Wiki)
	}
	if len(conf.Importance.Boosts) != 3 {
		t.Errorf("expected 3 boosts, got %d", len(conf.Importance.Boosts))
	}
}

func TestConfig_MissingOptionalFields(t *testing.T) {
	tomlContent := `
index_path = "test.bleve"
languages = ["en"]

[server]
host = "0.0.0.0"
port = 9889

[importance]
default = 1.0
`

	var conf config.Config
	err := toml.Unmarshal([]byte(tomlContent), &conf)
	if err != nil {
		t.Fatalf("failed to parse minimal config: %v", err)
	}

	// Optional fields should be zero/empty
	if conf.PBFPath != "" {
		t.Errorf("pbf_path should be empty, got %s", conf.PBFPath)
	}
	if conf.WikidataImportance != "" {
		t.Errorf("wikidata_importance should be empty, got %s", conf.WikidataImportance)
	}
	if conf.OntologyPath != "" {
		t.Errorf("ontology_path should be empty, got %s", conf.OntologyPath)
	}
	if conf.SimplificationTol != 0 {
		t.Errorf("simplification_tolerance should be 0, got %f", conf.SimplificationTol)
	}
	if conf.NameAnalyzer != "" {
		t.Errorf("name_analyzer should be empty, got %s", conf.NameAnalyzer)
	}
}

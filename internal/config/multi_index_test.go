package config_test

import (
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/pelletier/go-toml/v2"
)

func TestConfig_MultiIndex(t *testing.T) {
	tomlContent := `
index_paths = ["cities.bleve", "pois.bleve"]
languages = ["en"]

[server]
host = "127.0.0.1"
port = 9889

[importance]
default = 1.0
`

	var conf config.Config
	err := toml.Unmarshal([]byte(tomlContent), &conf)
	if err != nil {
		t.Fatalf("failed to parse multi-index config: %v", err)
	}

	if len(conf.IndexPaths) != 2 {
		t.Errorf("expected 2 index paths, got %d", len(conf.IndexPaths))
	}
	if conf.IndexPaths[0] != "cities.bleve" {
		t.Errorf("index_paths[0] = %s, want cities.bleve", conf.IndexPaths[0])
	}
	if conf.IndexPaths[1] != "pois.bleve" {
		t.Errorf("index_paths[1] = %s, want pois.bleve", conf.IndexPaths[1])
	}
}

package config

import "time"

const (
	// DefaultPort is the default server port if not specified in config.
	DefaultPort = 9889
	// DefaultPBF is the default PBF filename.
	DefaultPBF = "liechtenstein-latest.osm.pbf"

	// DefaultGeoBatchSize is the default batch size for geometry operations.
	DefaultGeoBatchSize = 200

	// DefaultCacheSize is the default LRU cache capacity in entries.
	DefaultCacheSize = 1000
	// DefaultCacheTTL is the default cache entry time-to-live.
	DefaultCacheTTL = 5 * time.Minute
)

type Config struct {
	IndexPath              string            `toml:"index_path"`          // Optional: path to Bleve index. If missing, falls back to PBF/PMTiles.
	PBFPath                string            `toml:"pbf_path"`            // Optional: path to PBF for direct (no-index) search
	PMTilesPath            string            `toml:"pmtiles_path"`        // Optional: path to PMTiles for direct (no-index) search
	WikidataImportance     string            `toml:"wikidata_importance"` // Optional: path to wikimedia_importance.tsv.gz
	OntologyPath           string            `toml:"ontology_path"`       // Optional: path to custom place type ontology CSV
	Languages              []string          `toml:"languages"`
	Importance             ImportanceWeights `toml:"importance"`
	GeometryMode           string            `toml:"geometry_mode"`            // "geopoint", "geoshape-bbox", "geoshape-simplified", "geoshape-full", "no-geo"
	SimplificationTol      float64           `toml:"simplification_tolerance"` // tolerance for SimplifyPreserveTopology
	NameAnalyzer           string            `toml:"name_analyzer"`            // "standard", "edge_ngram", "ngram"
	Server                 ServerConfig      `toml:"server"`
	NodesOnly              bool              `toml:"nodes_only"`
	DisableAltNames        bool              `toml:"disable_alt_names"`
	DisableImportance      bool              `toml:"disable_importance"`
	DisableKeyValues       bool              `toml:"disable_key_value"`
	OnlyNamed              bool              `toml:"only_named"`
	StoreMetadata          bool              `toml:"store_metadata"`
	StoreGeometry          bool              `toml:"store_geometry"`
	StoreAddress           bool              `toml:"store_address"`            // Opt-in: index addr:* tags for address search
	IndexWikidataRedirects bool              `toml:"index_wikidata_redirects"` // Opt-in: index Wikipedia redirect titles as alternate names
	PMTilesPostProcess     bool              `toml:"pmtiles_post_process"`     // Opt-in: perform precise intersection check for PMTiles (slow)

	// Build optimization configuration
	GeoBatchSize int `toml:"geo_batch_size"` // Batch size for geometry operations (default: 200, range: 50-1000)

	// Query cache configuration
	CacheEnabled bool          `toml:"cache_enabled"` // Enable query result caching (default: false)
	CacheSize    int           `toml:"cache_size"`    // LRU cache capacity in entries (default: 1000)
	CacheTTL     time.Duration `toml:"cache_ttl"`     // Cache entry time-to-live (default: 5m)
}

type ImportanceWeights struct {
	Boosts   []string `toml:"boosts"` // List of keys/values to prioritize (first = highest)
	Default  float64  `toml:"default"`
	PopBoost float64  `toml:"population_boost_weight"` // importance += math.Log(pop+1) * weight
	Capital  float64  `toml:"capital_boost"`
	Wiki     float64  `toml:"wikipedia_boost"`
}

type ServerConfig struct {
	Host           string   `toml:"host"`
	Port           int      `toml:"port"`
	AllowedOrigins []string `toml:"allowed_origins"`
}

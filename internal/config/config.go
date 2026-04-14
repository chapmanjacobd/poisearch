package config

const (
	// DefaultPort is the default server port if not specified in config.
	DefaultPort = 9889
	// DefaultPBF is the default PBF filename.
	DefaultPBF = "liechtenstein-latest.osm.pbf"
)

type Config struct {
	IndexPath           string            `toml:"index_path"`
	PBFPath             string            `toml:"pbf_path"`            // Optional: path to PBF for direct (no-index) search
	WikidataImportance  string            `toml:"wikidata_importance"` // Optional: path to wikimedia_importance.tsv.gz
	OntologyPath        string            `toml:"ontology_path"`       // Optional: path to custom place type ontology CSV
	Languages           []string          `toml:"languages"`
	Importance          ImportanceWeights `toml:"importance"`
	GeometryMode        string            `toml:"geometry_mode"`            // "geopoint", "geoshape-bbox", "geoshape-simplified", "geoshape-full", "no-geo"
	SimplificationTol   float64           `toml:"simplification_tolerance"` // tolerance for SimplifyPreserveTopology
	NameAnalyzer        string            `toml:"name_analyzer"`            // "standard", "edge_ngram", "ngram", "keyword"
	Server              ServerConfig      `toml:"server"`
	NodesOnly           bool              `toml:"nodes_only"`
	DisableAltNames     bool              `toml:"disable_alt_names"`
	DisableImportance   bool              `toml:"disable_importance"`
	DisableClassSubtype bool              `toml:"disable_class_subtype"`
	OnlyNamed           bool              `toml:"only_named"`
	StoreMetadata       bool              `toml:"store_metadata"`
	StoreGeometry       bool              `toml:"store_geometry"`
	StoreAddress        bool              `toml:"store_address"` // Opt-in: index addr:* tags for address search
	IndexWikidataRedirects bool           `toml:"index_wikidata_redirects"` // Opt-in: index Wikipedia redirect titles as alternate names
}

type ImportanceWeights struct {
	Place    map[string]float64 `toml:"place"`
	Amenity  map[string]float64 `toml:"amenity"`
	Highway  map[string]float64 `toml:"highway"`
	Shop     map[string]float64 `toml:"shop"`
	Tourism  map[string]float64 `toml:"tourism"`
	Leisure  map[string]float64 `toml:"leisure"`
	Historic map[string]float64 `toml:"historic"`
	Natural  map[string]float64 `toml:"natural"`
	Railway  map[string]float64 `toml:"railway"`
	Default  float64            `toml:"default"`
	PopBoost float64            `toml:"population_boost_weight"` // importance += math.Log(pop+1) * weight
	Capital  float64            `toml:"capital_boost"`
	Wiki     float64            `toml:"wikipedia_boost"`
}

type ServerConfig struct {
	Host           string   `toml:"host"`
	Port           int      `toml:"port"`
	AllowedOrigins []string `toml:"allowed_origins"`
}

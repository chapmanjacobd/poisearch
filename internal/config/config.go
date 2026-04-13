package config

type Config struct {
	IndexPath             string             `toml:"index_path"`
	Languages             []string           `toml:"languages"`
	Importance            ImportanceWeights  `toml:"importance"`
	GeometryMode          string             `toml:"geometry_mode"`           // "geopoint", "geoshape-bbox", "geoshape-simplified", "geoshape-full", "no-geo"
	SimplificationTol     float64            `toml:"simplification_tolerance"` // tolerance for SimplifyPreserveTopology
	Server                ServerConfig       `toml:"server"`
	NodesOnly             bool               `toml:"nodes_only"`
	DisableAltNames       bool               `toml:"disable_alt_names"`
	DisableImportance     bool               `toml:"disable_importance"`
	DisableClassSubtype   bool               `toml:"disable_class_subtype"`
}

type ImportanceWeights struct {
	Place    map[string]float64 `toml:"place"`
	Amenity  map[string]float64 `toml:"amenity"`
	Highway  map[string]float64 `toml:"highway"`
	Default  float64            `toml:"default"`
	PopBoost float64            `toml:"population_boost_factor"` // importance += math.Log(pop+1) / factor
	Capital  float64            `toml:"capital_boost"`
	Wiki     float64            `toml:"wikipedia_boost"`
}

type ServerConfig struct {
	Host string `toml:"host"`
	Port int    `toml:"port"`
}

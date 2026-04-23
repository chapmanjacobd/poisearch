package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/blevesearch/bleve/v2"
	blevesearch "github.com/blevesearch/bleve/v2/search"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

// ErrorResponse represents a structured error response.
type ErrorResponse struct {
	Error  string `json:"error"`
	Code   string `json:"code"`
	Status int    `json:"status"`
}

// SearchResponse wraps search results with pagination info.
type SearchResponse struct {
	Total    int64       `json:"total"`
	From     int         `json:"from"`
	Limit    int         `json:"limit"`
	Hits     []SearchHit `json:"hits"`
	Warnings []string    `json:"warnings,omitempty"`
}

// SearchHit represents a single search result hit.
type SearchHit struct {
	ID       string  `json:"id"`
	Score    float64 `json:"score"`
	Name     string  `json:"name,omitempty"`
	Key      string  `json:"key,omitempty"`
	Value    string  `json:"value,omitempty"`
	Geometry any     `json:"geometry,omitempty"`
}

type SearchDefaults struct {
	DefaultLimit int      `json:"default_limit"`
	MaxLimit     int      `json:"max_limit"`
	Languages    []string `json:"languages,omitempty"`
}

type SearchModeCapability struct {
	ID                 string   `json:"id"`
	Label              string   `json:"label"`
	Available          bool     `json:"available"`
	Default            bool     `json:"default,omitempty"`
	SupportsExactMatch bool     `json:"supports_exact_match,omitempty"`
	Sources            []string `json:"sources,omitempty"`
}

type SearchCapabilitiesResponse struct {
	Modes    []SearchModeCapability `json:"modes"`
	Defaults SearchDefaults         `json:"defaults"`
	Features []string               `json:"features"`
}

var (
	leadingHouseNumberPattern  = regexp.MustCompile(`^(?P<housenumber>\d[\p{L}\p{N}/-]*)[,\s]+(?P<street>.+)$`)
	trailingHouseNumberPattern = regexp.MustCompile(`^(?P<street>.+?)[,\s]+(?P<housenumber>\d[\p{L}\p{N}/-]*)$`)
)

func writeJSONError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	resp := ErrorResponse{
		Error:  message,
		Code:   code,
		Status: statusCode,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to encode error response", "error", err)
	}
}

func CORSMiddleware(next http.Handler, allowedOrigins []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			allowed := false
			if len(allowedOrigins) == 0 {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				allowed = true
			} else {
				for _, o := range allowedOrigins {
					if o == "*" || o == origin {
						w.Header().Set("Access-Control-Allow-Origin", o)
						allowed = true
						break
					}
				}
			}

			if allowed {
				w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}

// HandlerOptions contains configuration for API handlers.
type HandlerOptions struct {
	Indices      map[string]bleve.Index
	PBFPaths     map[string]string
	PMTilesPaths map[string]string
	Conf         *config.Config
	Cache        *search.QueryCache
}

func RegisterHandlers(mux *http.ServeMux, indices map[string]bleve.Index, conf *config.Config) {
	RegisterHandlersWithPBF(mux, HandlerOptions{
		Indices: indices,
		Conf:    conf,
	})
}

func RegisterHandlersWithPBF(mux *http.ServeMux, opts HandlerOptions) {
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if opts.Cache != nil {
			stats := opts.Cache.Stats()
			_, _ = w.Write(fmt.Appendf(nil, "OK (cache: %d hits, %d misses, %.2f%% hit rate)",
				stats.Hits, stats.Misses, stats.HitRate*100))
		} else {
			_, _ = w.Write([]byte("OK"))
		}
	})

	mux.HandleFunc("/capabilities", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(buildSearchCapabilities(opts)); err != nil {
			slog.Error("failed to encode capabilities response", "error", err)
		}
	})

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		mode := r.URL.Query().Get("mode")
		name := r.URL.Query().Get("index")

		// 1. Direct PBF Search
		if mode == "pbf" ||
			(mode == "" && len(opts.Indices) == 0 && len(opts.PMTilesPaths) == 0 && len(opts.PBFPaths) > 0) {

			path := ""
			if name != "" {
				path = opts.PBFPaths[name]
			} else {
				// Pick first available
				for _, p := range opts.PBFPaths {
					path = p
					break
				}
			}

			if path != "" {
				handlePBFSearch(w, r, path, opts.Conf)
				return
			}
		}

		// 2. Direct PMTiles Search
		if mode == "pmtiles" ||
			(mode == "" && len(opts.Indices) == 0 && len(opts.PBFPaths) == 0 && len(opts.PMTilesPaths) > 0) {

			path := ""
			if name != "" {
				path = opts.PMTilesPaths[name]
			} else {
				// Pick first available
				for _, p := range opts.PMTilesPaths {
					path = p
					break
				}
			}

			if path != "" {
				handlePMTilesSearch(w, r, path, opts.Conf)
				return
			}
		}

		// 3. Bleve Index Search
		var idx bleve.Index
		if name != "" {
			idx = opts.Indices[name]
		} else {
			// Pick first available
			for _, i := range opts.Indices {
				idx = i
				break
			}
		}

		if idx != nil {
			handleIndexSearch(w, r, idx, opts.Conf, opts.Cache)
			return
		}

		writeJSONError(
			w,
			http.StatusBadRequest,
			"no_source",
			"No search source found for requested mode/index.",
		)
	})
}

func buildSearchCapabilities(opts HandlerOptions) SearchCapabilitiesResponse {
	defaultMode := ""
	switch {
	case len(opts.Indices) > 0:
		defaultMode = "bleve"
	case len(opts.PBFPaths) > 0:
		defaultMode = "pbf"
	case len(opts.PMTilesPaths) > 0:
		defaultMode = "pmtiles"
	}

	modes := []SearchModeCapability{
		{
			ID:        "bleve",
			Label:     "Bleve index",
			Available: len(opts.Indices) > 0,
			Default:   defaultMode == "bleve",
			Sources:   sortedKeys(opts.Indices),
		},
		{
			ID:        "pbf",
			Label:     "Direct PBF",
			Available: len(opts.PBFPaths) > 0,
			Default:   defaultMode == "pbf",
			Sources:   sortedStringMapKeys(opts.PBFPaths),
		},
		{
			ID:                 "pmtiles",
			Label:              "Direct PMTiles",
			Available:          len(opts.PMTilesPaths) > 0,
			Default:            defaultMode == "pmtiles",
			SupportsExactMatch: true,
			Sources:            sortedStringMapKeys(opts.PMTilesPaths),
		},
	}

	var langs []string
	if opts.Conf != nil {
		langs = append([]string(nil), opts.Conf.Languages...)
	}
	sort.Strings(langs)

	return SearchCapabilitiesResponse{
		Modes: modes,
		Defaults: SearchDefaults{
			DefaultLimit: 100,
			MaxLimit:     1000,
			Languages:    langs,
		},
		Features: []string{
			"query",
			"pagination",
			"spatial",
			"bbox",
			"address",
			"metadata",
			"fuzzy",
			"prefix",
			"exact_match",
			"text_format",
		},
	}
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringMapKeys(m map[string]string) []string {
	return sortedKeys(m)
}

func handlePBFSearch(w http.ResponseWriter, r *http.Request, pbfPath string, conf *config.Config) {
	params := parseSearchParams(r, conf)

	res, err := osm.PBFSearch(pbfPath, params, conf)
	if err != nil {
		slog.Error("PBF search failed", "error", err, "query", params.Query)
		writeJSONError(w, http.StatusInternalServerError, "search_error", "PBF search failed: "+err.Error())
		return
	}

	writeJSONResponse(w, r, res, params)
}

func handlePMTilesSearch(w http.ResponseWriter, r *http.Request, pmtilesPath string, conf *config.Config) {
	params := parseSearchParams(r, conf)

	res, err := osm.PMTilesSearch(pmtilesPath, params, conf)
	if err != nil {
		slog.Error("PMTiles search failed", "error", err, "query", params.Query)
		writeJSONError(w, http.StatusInternalServerError, "search_error", "PMTiles search failed: "+err.Error())
		return
	}

	writeJSONResponse(w, r, res, params)
}

func writeJSONResponse(w http.ResponseWriter, r *http.Request, res *bleve.SearchResult, params search.SearchParams) {
	// Support text/plain output format (UNIX-pipe-friendly)
	format := r.URL.Query().Get("format")
	if format == "text" || strings.Contains(r.Header.Get("Accept"), "text/plain") {
		writeTextResponse(w, res, params.Langs)
	} else {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(res); err != nil {
			slog.Error("failed to encode JSON response", "error", err)
		}
	}
}

func handleIndexSearch(
	w http.ResponseWriter,
	r *http.Request,
	index bleve.Index,
	conf *config.Config,
	cache *search.QueryCache,
) {
	params := parseSearchParams(r, conf)

	// Try cache first (only for non-geo queries)
	if cache != nil && !search.IsGeoQuery(params) {
		cacheKey := search.BuildCacheKey(params)
		if cachedResult, found := cache.Get(cacheKey); found {
			slog.Debug("cache hit", "query", params.Query)
			// Return cached result directly as JSON
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(cachedResult); err != nil {
				slog.Error("failed to encode cached response", "error", err)
			}
			return
		}
	}

	res, err := search.Search(index, params)
	if err != nil {
		slog.Error("search failed", "error", err, "query", params.Query)
		writeJSONError(w, http.StatusInternalServerError, "search_error", "Search failed: "+err.Error())
		return
	}

	// Store in cache if enabled and not a geo query
	if cache != nil && !search.IsGeoQuery(params) {
		cacheKey := search.BuildCacheKey(params)
		serialized := search.SerializeResult(res)
		cache.Set(cacheKey, serialized)
		slog.Debug("cache store", "query", params.Query)
	}

	// Support text/plain output format (UNIX-pipe-friendly)
	format := r.URL.Query().Get("format")
	if format == "text" || strings.Contains(r.Header.Get("Accept"), "text/plain") {
		writeTextResponse(w, res, params.Langs)
	} else {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(res); err != nil {
			slog.Error("failed to encode JSON response", "error", err)
		}
	}
}

func parseFloatPtr(s string) *float64 {
	if s == "" {
		return nil
	}
	l, err := strconv.ParseFloat(s, 64)
	if err == nil {
		return &l
	}
	return nil
}

func parseBool(s string) bool {
	return s == "1" || s == "true"
}

func parseCommaList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func normalizeStreetAddress(street, houseNumber string) (string, string) {
	street = strings.TrimSpace(street)
	houseNumber = strings.TrimSpace(houseNumber)

	matches := leadingHouseNumberPattern.FindStringSubmatch(street)
	if len(matches) == 3 {
		inferredStreet := strings.TrimSpace(matches[2])
		inferredHouseNumber := strings.TrimSpace(matches[1])
		if houseNumber == "" {
			return inferredStreet, inferredHouseNumber
		}
		if strings.EqualFold(houseNumber, inferredHouseNumber) {
			return inferredStreet, houseNumber
		}
		return street, houseNumber
	}

	matches = trailingHouseNumberPattern.FindStringSubmatch(street)
	if len(matches) != 3 {
		return street, houseNumber
	}

	inferredStreet := strings.TrimSpace(matches[1])
	inferredHouseNumber := strings.TrimSpace(matches[2])
	if houseNumber == "" {
		return inferredStreet, inferredHouseNumber
	}
	if strings.EqualFold(houseNumber, inferredHouseNumber) {
		return inferredStreet, houseNumber
	}

	return street, houseNumber
}

func parseSearchParams(r *http.Request, conf *config.Config) search.SearchParams {
	q := r.URL.Query()

	limit := 100
	if l, err := strconv.Atoi(q.Get("limit")); err == nil {
		limit = l
	}
	if limit > 1000 {
		limit = 1000
	}

	from := 0
	if f, err := strconv.Atoi(q.Get("from")); err == nil && f >= 0 {
		from = f
	}

	var langs []string
	if l := q.Get("langs"); l != "" {
		langs = strings.Split(l, ",")
	} else {
		langs = conf.Languages
	}

	street, houseNumber := normalizeStreetAddress(q.Get("street"), q.Get("housenumber"))

	return search.SearchParams{
		Query:        q.Get("q"),
		Lat:          parseFloatPtr(q.Get("lat")),
		Lon:          parseFloatPtr(q.Get("lon")),
		Radius:       q.Get("radius"),
		MinLat:       parseFloatPtr(q.Get("min_lat")),
		MaxLat:       parseFloatPtr(q.Get("max_lat")),
		MinLon:       parseFloatPtr(q.Get("min_lon")),
		MaxLon:       parseFloatPtr(q.Get("max_lon")),
		Limit:        limit,
		From:         from,
		Langs:        langs,
		GeoMode:      conf.GeometryMode,
		Fuzzy:        parseBool(q.Get("fuzzy")),
		Prefix:       parseBool(q.Get("prefix")),
		Key:          q.Get("key"),
		Value:        q.Get("value"),
		Keys:         parseCommaList(q.Get("keys")),
		Values:       parseCommaList(q.Get("values")),
		Street:       street,
		HouseNumber:  houseNumber,
		Postcode:     q.Get("postcode"),
		City:         q.Get("city"),
		Country:      q.Get("country"),
		Floor:        q.Get("floor"),
		Unit:         q.Get("unit"),
		Level:        q.Get("level"),
		Phone:        q.Get("phone"),
		Wheelchair:   q.Get("wheelchair"),
		OpeningHours: q.Get("opening_hours"),
		ExactMatch:   parseBool(q.Get("exact_match")),
		Analyzer:     conf.NameAnalyzer,
		PopBoost:     conf.Importance.PopBoost,
	}
}

// writeTextResponse writes search results in a simple key-value format
// suitable for piping through UNIX tools like grep, awk, etc.
func writeTextResponse(w http.ResponseWriter, res *bleve.SearchResult, langs []string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	for i, hit := range res.Hits {
		if i > 0 {
			fmt.Fprintln(w) // Blank line between entities
		}
		writeBasicInfo(w, hit)
		writeCoreFields(w, hit)
		writeAlternateNames(w, hit)
		writeLocalizedNames(w, hit, langs)
		writeGeometry(w, hit)
		writeAddress(w, hit)
		writeMetadataFields(w, hit)
		writeDistance(w, hit)
	}

	fmt.Fprintf(w, "\n---\ntotal: %d\nreturned: %d\n", res.Total, len(res.Hits))
}

func writeBasicInfo(w http.ResponseWriter, hit *blevesearch.DocumentMatch) {
	fmt.Fprintf(w, "id: %s\n", hit.ID)
	fmt.Fprintf(w, "score: %.6f\n", hit.Score)
}

func writeCoreFields(w http.ResponseWriter, hit *blevesearch.DocumentMatch) {
	if name, ok := hit.Fields["name"].(string); ok && name != "" {
		fmt.Fprintf(w, "name: %s\n", name)
	}
	if key, ok := hit.Fields["key"].(string); ok && key != "" {
		fmt.Fprintf(w, "key: %s\n", key)
	}
	if value, ok := hit.Fields["value"].(string); ok && value != "" {
		fmt.Fprintf(w, "value: %s\n", value)
	}
	if keys, ok := hit.Fields["keys"].(string); ok && keys != "" {
		fmt.Fprintf(w, "keys: %s\n", keys)
	}
	if values, ok := hit.Fields["values"].(string); ok && values != "" {
		fmt.Fprintf(w, "values: %s\n", values)
	}
	if importance, ok := hit.Fields["importance"].(float64); ok {
		fmt.Fprintf(w, "importance: %.6f\n", importance)
	}
}

func writeAlternateNames(w http.ResponseWriter, hit *blevesearch.DocumentMatch) {
	for _, altKey := range []string{"alt_name", "old_name", "short_name"} {
		if val, ok := hit.Fields[altKey].(string); ok && val != "" {
			fmt.Fprintf(w, "%s: %s\n", altKey, val)
		}
	}
}

func writeLocalizedNames(w http.ResponseWriter, hit *blevesearch.DocumentMatch, langs []string) {
	for _, lang := range langs {
		if val, ok := hit.Fields["name:"+lang].(string); ok && val != "" {
			fmt.Fprintf(w, "name:%s: %s\n", lang, val)
		}
	}
}

func writeGeometry(w http.ResponseWriter, hit *blevesearch.DocumentMatch) {
	if geom, ok := hit.Fields["geometry"].(map[string]any); ok {
		if lat, ok := geom["lat"].(float64); ok {
			fmt.Fprintf(w, "lat: %.5f\n", lat)
		}
		if lon, ok := geom["lon"].(float64); ok {
			fmt.Fprintf(w, "lon: %.5f\n", lon)
		}
	}
}

func writeAddress(w http.ResponseWriter, hit *blevesearch.DocumentMatch) {
	if displayAddr, ok := hit.Fields["display_address"].(string); ok && displayAddr != "" {
		fmt.Fprintf(w, "address: %s\n", displayAddr)
	} else {
		for _, addrKey := range []string{
			"addr:housenumber", "addr:street", "addr:city", "addr:postcode",
			"addr:country", "addr:state", "addr:district", "addr:suburb",
			"addr:neighbourhood", "addr:floor", "addr:unit", "level",
		} {
			if val, ok := hit.Fields[addrKey].(string); ok && val != "" {
				fmt.Fprintf(w, "%s: %s\n", addrKey, val)
			}
		}
	}
}

func writeMetadataFields(w http.ResponseWriter, hit *blevesearch.DocumentMatch) {
	for _, metaKey := range []string{"phone", "wheelchair", "opening_hours"} {
		if val, ok := hit.Fields[metaKey].(string); ok && val != "" {
			fmt.Fprintf(w, "%s: %s\n", metaKey, val)
		}
	}
}

func writeDistance(w http.ResponseWriter, hit *blevesearch.DocumentMatch) {
	if dist, ok := hit.Fields["distance_meters"].(int); ok {
		fmt.Fprintf(w, "distance_meters: %d\n", dist)
	}
}

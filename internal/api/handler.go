package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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

// SearchResponse is the normalized response returned by every backend.
// total_hits is kept as an alias of total for backwards compatibility.
type SearchResponse struct {
	Total     int64                        `json:"total"`
	TotalHits uint64                       `json:"total_hits"`
	From      int                          `json:"from"`
	Limit     int                          `json:"limit"`
	Hits      []*blevesearch.DocumentMatch `json:"hits"`
	Status    *bleve.SearchStatus          `json:"status"`
	Cost      uint64                       `json:"cost"`
	MaxScore  float64                      `json:"max_score"`
	Took      int64                        `json:"took"`
	Facets    any                          `json:"facets"`
	Warnings  []string                     `json:"warnings,omitempty"`
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
			slog.ErrorContext(r.Context(), "failed to encode capabilities response", "error", err)
		}
	})

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		mode := r.URL.Query().Get("mode")
		name := r.URL.Query().Get("index")

		switch mode {
		case "pbf":
			path, ok := lookupSource(opts.PBFPaths, name)
			if !ok {
				writeJSONError(w, http.StatusBadRequest, "no_source", "No PBF source found for requested index.")
				return
			}
			handlePBFSearch(w, r, path, opts.Conf)
			return
		case "pmtiles":
			path, ok := lookupSource(opts.PMTilesPaths, name)
			if !ok {
				writeJSONError(w, http.StatusBadRequest, "no_source", "No PMTiles source found for requested index.")
				return
			}
			handlePMTilesSearch(w, r, path, opts.Conf)
			return
		case "bleve", "":
			// fall through to the default selection logic below
		default:
			writeJSONError(w, http.StatusBadRequest, "invalid_mode", "Unknown search mode: "+mode)
			return
		}

		// Explicit Bleve mode, or find the backend that owns the requested index.
		if mode == "bleve" {
			idx, ok := lookupIndex(opts.Indices, name)
			if !ok {
				writeJSONError(w, http.StatusBadRequest, "no_source", "No Bleve index found for requested index.")
				return
			}
			handleIndexSearch(w, r, idx, opts.Conf, opts.Cache)
			return
		}

		if name != "" {
			if idx, ok := lookupIndex(opts.Indices, name); ok {
				handleIndexSearch(w, r, idx, opts.Conf, opts.Cache)
				return
			}
			if path, ok := lookupSource(opts.PBFPaths, name); ok {
				handlePBFSearch(w, r, path, opts.Conf)
				return
			}
			if path, ok := lookupSource(opts.PMTilesPaths, name); ok {
				handlePMTilesSearch(w, r, path, opts.Conf)
				return
			}
			writeJSONError(w, http.StatusBadRequest, "no_source", "No search source found for requested index.")
			return
		}

		// Auto-select only when the caller did not force a specific backend.
		if mode == "" {
			if idx, ok := lookupIndex(opts.Indices, ""); ok {
				handleIndexSearch(w, r, idx, opts.Conf, opts.Cache)
				return
			}
			if path, ok := lookupSource(opts.PBFPaths, ""); ok {
				handlePBFSearch(w, r, path, opts.Conf)
				return
			}
			if path, ok := lookupSource(opts.PMTilesPaths, ""); ok {
				handlePMTilesSearch(w, r, path, opts.Conf)
				return
			}
		}

		writeJSONError(
			w,
			http.StatusBadRequest,
			"no_source",
			"No search source found for requested mode/index.",
		)
	})
}

func lookupIndex(indices map[string]bleve.Index, name string) (bleve.Index, bool) {
	if len(indices) == 0 {
		return nil, false
	}
	if name != "" {
		idx, ok := indices[name]
		return idx, ok
	}
	for _, key := range sortedKeys(indices) {
		return indices[key], true
	}
	return nil, false
}

func lookupSource(paths map[string]string, name string) (string, bool) {
	if len(paths) == 0 {
		return "", false
	}
	if name != "" {
		path, ok := paths[name]
		return path, ok
	}
	for _, key := range sortedStringMapKeys(paths) {
		return paths[key], true
	}
	return "", false
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
	params, err := parseSearchParamsWithError(r, conf)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_parameter", err.Error())
		return
	}

	start := time.Now()
	res, err := osm.PBFSearch(pbfPath, params, conf)
	if err != nil {
		slog.ErrorContext(r.Context(), "PBF search failed", "error", err, "query", params.Query)
		writeJSONError(w, http.StatusInternalServerError, "search_error", "PBF search failed: "+err.Error())
		return
	}

	writeJSONResponse(w, r, res, params, time.Since(start))
}

func handlePMTilesSearch(w http.ResponseWriter, r *http.Request, pmtilesPath string, conf *config.Config) {
	params, err := parseSearchParamsWithError(r, conf)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_parameter", err.Error())
		return
	}

	start := time.Now()
	res, err := osm.PMTilesSearch(pmtilesPath, params, conf)
	if err != nil {
		slog.ErrorContext(r.Context(), "PMTiles search failed", "error", err, "query", params.Query)
		writeJSONError(w, http.StatusInternalServerError, "search_error", "PMTiles search failed: "+err.Error())
		return
	}

	writeJSONResponse(w, r, res, params, time.Since(start))
}

func writeJSONResponse(
	w http.ResponseWriter,
	r *http.Request,
	res *bleve.SearchResult,
	params search.SearchParams,
	elapsed time.Duration,
) {
	// Support text/plain output format (UNIX-pipe-friendly)
	format := r.URL.Query().Get("format")
	if format == "text" || strings.Contains(r.Header.Get("Accept"), "text/plain") {
		writeTextResponse(w, res, params.Langs)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(normalizeResponse(res, params, elapsed)); err != nil {
		slog.ErrorContext(r.Context(), "failed to encode JSON response", "error", err)
	}
}

func normalizeResponse(
	res *bleve.SearchResult,
	params search.SearchParams,
	elapsed time.Duration,
) SearchResponse {
	normalized := SearchResponse{
		From:   max(params.From, 0),
		Limit:  effectiveLimit(params.Limit),
		Hits:   []*blevesearch.DocumentMatch{},
		Facets: res.Facets,
	}
	if res == nil {
		normalized.Status = &bleve.SearchStatus{Total: 1, Successful: 1}
		normalized.Took = elapsed.Nanoseconds()
		return normalized
	}

	normalized.Total = int64(res.Total)
	normalized.TotalHits = res.Total
	if res.Hits != nil {
		normalized.Hits = res.Hits
	}
	normalized.Status = res.Status
	if normalized.Status == nil {
		normalized.Status = &bleve.SearchStatus{Total: 1, Successful: 1}
	}
	normalized.Cost = res.Cost
	normalized.MaxScore = res.MaxScore
	for _, hit := range res.Hits {
		normalized.MaxScore = max(normalized.MaxScore, hit.Score)
	}
	normalized.Took = res.Took.Nanoseconds()
	if normalized.Took == 0 {
		normalized.Took = elapsed.Nanoseconds()
	}
	return normalized
}

func effectiveLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	return min(limit, 1000)
}

func handleIndexSearch(
	w http.ResponseWriter,
	r *http.Request,
	index bleve.Index,
	conf *config.Config,
	cache *search.QueryCache,
) {
	params, err := parseSearchParamsWithError(r, conf)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_parameter", err.Error())
		return
	}

	// Try cache first (only for non-geo queries)
	if cache != nil && !search.IsGeoQuery(params) {
		cacheKey := search.BuildCacheKey(params)
		if cachedResult, found := cache.Get(cacheKey); found {
			slog.DebugContext(r.Context(), "cache hit", "query", params.Query)
			w.Header().Set("Content-Type", "application/json")
			if encodeErr := json.NewEncoder(w).Encode(cachedResponse(cachedResult, params)); encodeErr != nil {
				slog.ErrorContext(r.Context(), "failed to encode cached response", "error", encodeErr)
			}
			return
		}
	}

	start := time.Now()
	res, err := search.Search(index, params)
	if err != nil {
		slog.ErrorContext(r.Context(), "search failed", "error", err, "query", params.Query)
		writeJSONError(w, http.StatusInternalServerError, "search_error", "Search failed: "+err.Error())
		return
	}

	// Store in cache if enabled and not a geo query
	if cache != nil && !search.IsGeoQuery(params) {
		cacheKey := search.BuildCacheKey(params)
		serialized := search.SerializeResult(res)
		cache.Set(cacheKey, serialized)
		slog.DebugContext(r.Context(), "cache store", "query", params.Query)
	}

	writeJSONResponse(w, r, res, params, time.Since(start))
}

func cachedResponse(cached *search.SerializedResult, params search.SearchParams) SearchResponse {
	resp := SearchResponse{
		From:   max(params.From, 0),
		Limit:  effectiveLimit(params.Limit),
		Hits:   []*blevesearch.DocumentMatch{},
		Status: &bleve.SearchStatus{Total: 1, Successful: 1},
	}
	if cached == nil {
		return resp
	}
	resp.Total = cached.Total
	resp.TotalHits = uint64(max(cached.Total, 0))
	for _, hit := range cached.Hits {
		fields := map[string]any{}
		if hit.Name != "" {
			fields["name"] = hit.Name
		}
		if hit.Key != "" {
			fields["key"] = hit.Key
		}
		if hit.Value != "" {
			fields["value"] = hit.Value
		}
		if hit.Lat != 0 || hit.Lon != 0 {
			fields["geometry"] = []float64{hit.Lon, hit.Lat}
		}
		resp.Hits = append(resp.Hits, &blevesearch.DocumentMatch{
			ID:     hit.ID,
			Score:  hit.Score,
			Fields: fields,
		})
	}
	return resp
}

func parseBool(s string) bool {
	return s == "1" || s == "true"
}

func parseCommaList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeStreetAddress(street, houseNumber string) (normalizedStreet, normalizedHouseNumber string) {
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

func isKnownSearchParam(key string) bool {
	switch key {
	case "q", "mode", "index", "format",
		"lat", "lon", "radius",
		"min_lat", "max_lat", "min_lon", "max_lon",
		"limit", "from", "langs", "fuzzy", "prefix", "exact_match",
		"key", "value", "keys", "values", "type",
		"street", "housenumber", "postcode", "city", "country",
		"floor", "unit", "level",
		"phone", "wheelchair", "opening_hours":
		return true
	default:
		return false
	}
}

func parseSearchParams(r *http.Request, conf *config.Config) search.SearchParams {
	params, _ := parseSearchParamsWithError(r, conf)
	return params
}

func parseSearchParamsWithError(r *http.Request, conf *config.Config) (search.SearchParams, error) {
	q := r.URL.Query()
	limit, from, err := parsePagination(q)
	if err != nil {
		return search.SearchParams{}, err
	}

	spatial, err := parseSpatialParams(q)
	if err != nil {
		return search.SearchParams{}, err
	}

	var langs []string
	if l := q.Get("langs"); l != "" {
		langs = parseCommaList(l)
	} else {
		langs = conf.Languages
	}

	street, houseNumber := normalizeStreetAddress(q.Get("street"), q.Get("housenumber"))

	value := q.Get("value")
	if value == "" {
		value = q.Get("type")
	}

	return search.SearchParams{
		Query:        q.Get("q"),
		Lat:          spatial.lat,
		Lon:          spatial.lon,
		Radius:       spatial.radius,
		MinLat:       spatial.minLat,
		MaxLat:       spatial.maxLat,
		MinLon:       spatial.minLon,
		MaxLon:       spatial.maxLon,
		Limit:        limit,
		From:         from,
		Langs:        langs,
		GeoMode:      conf.GeometryMode,
		Fuzzy:        parseBool(q.Get("fuzzy")),
		Prefix:       parseBool(q.Get("prefix")),
		Key:          q.Get("key"),
		Value:        value,
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
		Tags:         parseTagFilters(q),
	}, nil
}

func parsePagination(q url.Values) (limit, from int, err error) {
	limit = 100
	if raw := q.Get("limit"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 1 {
			return 0, 0, errors.New("limit must be a positive integer")
		}
		limit = min(value, 1000)
	}

	if raw := q.Get("from"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 0 {
			return 0, 0, errors.New("from must be a non-negative integer")
		}
		from = value
	}
	return limit, from, nil
}

type spatialParams struct {
	lat, lon                       *float64
	minLat, maxLat, minLon, maxLon *float64
	radius                         string
}

func parseSpatialParams(q url.Values) (spatialParams, error) {
	lat, err := parseOptionalFloat(q.Get("lat"), "lat")
	if err != nil {
		return spatialParams{}, err
	}
	lon, err := parseOptionalFloat(q.Get("lon"), "lon")
	if err != nil {
		return spatialParams{}, err
	}
	minLat, err := parseOptionalFloat(q.Get("min_lat"), "min_lat")
	if err != nil {
		return spatialParams{}, err
	}
	maxLat, err := parseOptionalFloat(q.Get("max_lat"), "max_lat")
	if err != nil {
		return spatialParams{}, err
	}
	minLon, err := parseOptionalFloat(q.Get("min_lon"), "min_lon")
	if err != nil {
		return spatialParams{}, err
	}
	maxLon, err := parseOptionalFloat(q.Get("max_lon"), "max_lon")
	if err != nil {
		return spatialParams{}, err
	}

	radius := q.Get("radius")
	if radius != "" {
		if _, radiusErr := search.ParseRadiusMeters(radius); radiusErr != nil {
			return spatialParams{}, radiusErr
		}
	}

	// A radius search needs a center and a radius. A bbox search needs all four
	// corners. Partial or invalid spatial parameters must not silently broaden
	// the query to an unfiltered search. Near queries resolve their own center,
	// so they may supply only a radius.
	isNearQuery := search.IsNearQuery(q.Get("q"))
	if !isNearQuery && (lat != nil || lon != nil || radius != "") {
		if lat == nil || lon == nil {
			return spatialParams{}, errors.New("lat and lon are both required for a radius search")
		}
		if radius == "" {
			return spatialParams{}, errors.New("radius is required when lat/lon are provided")
		}
	}
	if lat != nil && (*lat < -90 || *lat > 90) {
		return spatialParams{}, errors.New("lat must be between -90 and 90")
	}
	if lon != nil && (*lon < -180 || *lon > 180) {
		return spatialParams{}, errors.New("lon must be between -180 and 180")
	}

	bboxSet := []*float64{minLat, maxLat, minLon, maxLon}
	bboxProvided := 0
	for _, value := range bboxSet {
		if value != nil {
			bboxProvided++
		}
	}
	if bboxProvided > 0 && bboxProvided < 4 {
		return spatialParams{}, errors.New(
			"min_lat, max_lat, min_lon, and max_lon are all required for a bbox search",
		)
	}
	if bboxProvided == 4 {
		if err := validateBBox(*minLat, *maxLat, *minLon, *maxLon); err != nil {
			return spatialParams{}, err
		}
	}

	return spatialParams{
		lat: lat, lon: lon, radius: radius,
		minLat: minLat, maxLat: maxLat, minLon: minLon, maxLon: maxLon,
	}, nil
}

func validateBBox(minLat, maxLat, minLon, maxLon float64) error {
	if minLat < -90 || maxLat > 90 {
		return errors.New("bbox latitudes must be between -90 and 90")
	}
	if minLon < -180 || maxLon > 180 {
		return errors.New("bbox longitudes must be between -180 and 180")
	}
	if minLat > maxLat {
		return errors.New("min_lat must be less than or equal to max_lat")
	}
	if minLon > maxLon {
		return errors.New("min_lon must be less than or equal to max_lon")
	}
	return nil
}

func parseOptionalFloat(raw, name string) (*float64, error) {
	if raw == "" {
		//nolint:nilnil // absence of an optional parameter is represented by nil
		return nil, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, fmt.Errorf("%s must be a number", name)
	}
	return &value, nil
}

func parseTagFilters(q url.Values) map[string]string {
	tags := make(map[string]string)
	for key, values := range q {
		if isKnownSearchParam(key) || len(values) == 0 {
			continue
		}
		if strings.HasPrefix(key, "_") || strings.HasPrefix(key, "utm_") {
			continue
		}
		if values[0] != "" {
			tags[key] = values[0]
		}
	}
	if len(tags) == 0 {
		return nil
	}
	return tags
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
	lat, lon, ok := search.HitLatLon(hit)
	if !ok {
		return
	}
	fmt.Fprintf(w, "lat: %.5f\n", lat)
	fmt.Fprintf(w, "lon: %.5f\n", lon)
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
	switch dist := hit.Fields["distance_meters"].(type) {
	case int:
		fmt.Fprintf(w, "distance_meters: %d\n", dist)
	case int64:
		fmt.Fprintf(w, "distance_meters: %d\n", dist)
	case float64:
		fmt.Fprintf(w, "distance_meters: %.0f\n", dist)
	}
}

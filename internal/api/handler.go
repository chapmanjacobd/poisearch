package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/blevesearch/bleve/v2"
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
	Class    string  `json:"class,omitempty"`
	Subtype  string  `json:"subtype,omitempty"`
	Geometry any     `json:"geometry,omitempty"`
}

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

func RegisterHandlers(mux *http.ServeMux, index bleve.Index, conf *config.Config) {
	RegisterHandlersWithPBF(mux, index, conf, "", nil)
}

func RegisterHandlersWithPBF(
	mux *http.ServeMux,
	index bleve.Index,
	conf *config.Config,
	pbfPath string,
	cache *search.QueryCache,
) {
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if cache != nil {
			stats := cache.Stats()
			_, _ = w.Write(fmt.Appendf(nil, "OK (cache: %d hits, %d misses, %.2f%% hit rate)",
				stats.Hits, stats.Misses, stats.HitRate*100))
		} else {
			_, _ = w.Write([]byte("OK"))
		}
	})

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		// If PBF path is configured and ?mode=pbf is set, use direct PBF search
		if pbfPath != "" && r.URL.Query().Get("mode") == "pbf" {
			handlePBFSearch(w, r, pbfPath, conf)
			return
		}
		handleIndexSearch(w, r, index, conf, cache)
	})
}

func handlePBFSearch(w http.ResponseWriter, r *http.Request, pbfPath string, conf *config.Config) {
	q := r.URL.Query().Get("q")
	latStr := r.URL.Query().Get("lat")
	lonStr := r.URL.Query().Get("lon")
	radius := r.URL.Query().Get("radius")
	bbox := r.URL.Query().Get("bbox") // bbox=minLat,minLon,maxLat,maxLon
	limitStr := r.URL.Query().Get("limit")
	fromStr := r.URL.Query().Get("from")
	class := r.URL.Query().Get("class")
	subtype := r.URL.Query().Get("subtype")
	format := r.URL.Query().Get("format")

	var lat, lon *float64
	if latStr != "" {
		l, err := strconv.ParseFloat(latStr, 64)
		if err == nil {
			lat = &l
		}
	}
	if lonStr != "" {
		l, err := strconv.ParseFloat(lonStr, 64)
		if err == nil {
			lon = &l
		}
	}

	// Parse bbox parameter
	var minLat, maxLat, minLon, maxLon *float64
	if bbox != "" {
		parts := strings.Split(bbox, ",")
		if len(parts) == 4 {
			minLatVal, err1 := strconv.ParseFloat(parts[0], 64)
			minLonVal, err2 := strconv.ParseFloat(parts[1], 64)
			maxLatVal, err3 := strconv.ParseFloat(parts[2], 64)
			maxLonVal, err4 := strconv.ParseFloat(parts[3], 64)
			if err1 == nil && err2 == nil && err3 == nil && err4 == nil {
				minLat = &minLatVal
				minLon = &minLonVal
				maxLat = &maxLatVal
				maxLon = &maxLonVal
			}
		}
	}

	limit := 10
	if limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err == nil {
			limit = l
		}
	}

	from := 0
	if fromStr != "" {
		f, err := strconv.Atoi(fromStr)
		if err == nil && f >= 0 {
			from = f
		}
	}

	params := search.SearchParams{
		Query:   q,
		Lat:     lat,
		Lon:     lon,
		Radius:  radius,
		MinLat:  minLat,
		MaxLat:  maxLat,
		MinLon:  minLon,
		MaxLon:  maxLon,
		Limit:   limit,
		From:    from,
		Langs:   conf.Languages,
		GeoMode: conf.GeometryMode,
		Class:   class,
		Subtype: subtype,
	}

	res, err := osm.PBFSearch(pbfPath, params, conf)
	if err != nil {
		slog.Error("PBF search failed", "error", err, "query", q)
		writeJSONError(w, http.StatusInternalServerError, "search_error", "PBF search failed: "+err.Error())
		return
	}

	if format == "text" || strings.Contains(r.Header.Get("Accept"), "text/plain") {
		writeTextResponse(w, res, conf.Languages)
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

func parseSearchParams(r *http.Request, conf *config.Config) search.SearchParams {
	q := r.URL.Query().Get("q")
	latStr := r.URL.Query().Get("lat")
	lonStr := r.URL.Query().Get("lon")
	radius := r.URL.Query().Get("radius")
	limitStr := r.URL.Query().Get("limit")
	fromStr := r.URL.Query().Get("from")
	langsStr := r.URL.Query().Get("langs")
	fuzzy := r.URL.Query().Get("fuzzy") == "1" || r.URL.Query().Get("fuzzy") == "true"
	prefix := r.URL.Query().Get("prefix") == "1" || r.URL.Query().Get("prefix") == "true"
	class := r.URL.Query().Get("class")
	subtype := r.URL.Query().Get("subtype")
	classes := r.URL.Query().Get("classes")   // comma-separated multi-class
	subtypes := r.URL.Query().Get("subtypes") // comma-separated multi-subtype

	// Address search params
	street := r.URL.Query().Get("street")
	housenumber := r.URL.Query().Get("housenumber")
	postcode := r.URL.Query().Get("postcode")
	city := r.URL.Query().Get("city")
	country := r.URL.Query().Get("country")

	var lat, lon *float64
	if latStr != "" {
		l, err := strconv.ParseFloat(latStr, 64)
		if err == nil {
			lat = &l
		}
	}
	if lonStr != "" {
		l, err := strconv.ParseFloat(lonStr, 64)
		if err == nil {
			lon = &l
		}
	}

	limit := 10
	if limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err == nil {
			limit = l
		}
	}

	from := 0
	if fromStr != "" {
		f, err := strconv.Atoi(fromStr)
		if err == nil && f >= 0 {
			from = f
		}
	}

	var langs []string
	if langsStr != "" {
		langs = strings.Split(langsStr, ",")
	} else {
		langs = conf.Languages
	}

	// Parse multi-value filters
	var classList []string
	if classes != "" {
		classList = strings.Split(classes, ",")
	}
	var subtypeList []string
	if subtypes != "" {
		subtypeList = strings.Split(subtypes, ",")
	}

	return search.SearchParams{
		Query:       q,
		Lat:         lat,
		Lon:         lon,
		Radius:      radius,
		Limit:       limit,
		From:        from,
		Langs:       langs,
		GeoMode:     conf.GeometryMode,
		Fuzzy:       fuzzy,
		Prefix:      prefix,
		Class:       class,
		Subtype:     subtype,
		Classes:     classList,
		Subtypes:    subtypeList,
		Street:      street,
		HouseNumber: housenumber,
		Postcode:    postcode,
		City:        city,
		Country:     country,
		Analyzer:    conf.NameAnalyzer,
	}
}

// writeTextResponse writes search results in a simple key-value format
// suitable for piping through UNIX tools like grep, awk, etc.
//
//nolint:revive // Response formatting requires handling all fields, complexity is inherent
func writeTextResponse(w http.ResponseWriter, res *bleve.SearchResult, langs []string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	for i, hit := range res.Hits {
		if i > 0 {
			fmt.Fprintln(w) // Blank line between entities
		}

		// Basic info
		fmt.Fprintf(w, "id: %s\n", hit.ID)
		fmt.Fprintf(w, "score: %.6f\n", hit.Score)

		// Extract fields
		if name, ok := hit.Fields["name"].(string); ok && name != "" {
			fmt.Fprintf(w, "name: %s\n", name)
		}
		if class, ok := hit.Fields["class"].(string); ok && class != "" {
			fmt.Fprintf(w, "class: %s\n", class)
		}
		if subtype, ok := hit.Fields["subtype"].(string); ok && subtype != "" {
			fmt.Fprintf(w, "subtype: %s\n", subtype)
		}
		if classes, ok := hit.Fields["classes"].(string); ok && classes != "" {
			fmt.Fprintf(w, "classes: %s\n", classes)
		}
		if subtypes, ok := hit.Fields["subtypes"].(string); ok && subtypes != "" {
			fmt.Fprintf(w, "subtypes: %s\n", subtypes)
		}
		if importance, ok := hit.Fields["importance"].(float64); ok {
			fmt.Fprintf(w, "importance: %.6f\n", importance)
		}

		// Alternate names
		for _, altKey := range []string{"alt_name", "old_name", "short_name"} {
			if val, ok := hit.Fields[altKey].(string); ok && val != "" {
				fmt.Fprintf(w, "%s: %s\n", altKey, val)
			}
		}

		// Localized names
		for _, lang := range langs {
			if val, ok := hit.Fields["name:"+lang].(string); ok && val != "" {
				fmt.Fprintf(w, "name:%s: %s\n", lang, val)
			}
		}

		// Geometry (simplified)
		if geom, ok := hit.Fields["geometry"].(map[string]any); ok {
			if lat, ok := geom["lat"].(float64); ok {
				fmt.Fprintf(w, "lat: %.5f\n", lat)
			}
			if lon, ok := geom["lon"].(float64); ok {
				fmt.Fprintf(w, "lon: %.5f\n", lon)
			}
		}

		// Address fields
		for _, addrKey := range []string{"addr:housenumber", "addr:street", "addr:city", "addr:postcode", "addr:country", "addr:state", "addr:district", "addr:suburb", "addr:neighbourhood"} {
			if val, ok := hit.Fields[addrKey].(string); ok && val != "" {
				fmt.Fprintf(w, "%s: %s\n", addrKey, val)
			}
		}

		// Distance (if radius search was used)
		if dist, ok := hit.Fields["distance_meters"].(int); ok {
			fmt.Fprintf(w, "distance_meters: %d\n", dist)
		}
	}

	// Summary
	fmt.Fprintf(w, "\n---\ntotal: %d\nreturned: %d\n", res.Total, len(res.Hits))
}

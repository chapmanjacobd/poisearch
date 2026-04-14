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

func RegisterHandlers(mux *http.ServeMux, index bleve.Index, conf *config.Config) {
	RegisterHandlersWithPBF(mux, index, conf, "")
}

func RegisterHandlersWithPBF(mux *http.ServeMux, index bleve.Index, conf *config.Config, pbfPath string) {
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		// If PBF path is configured and ?mode=pbf is set, use direct PBF search
		if pbfPath != "" && r.URL.Query().Get("mode") == "pbf" {
			handlePBFSearch(w, r, pbfPath, conf)
			return
		}
		handleIndexSearch(w, r, index, conf)
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

func handleIndexSearch(w http.ResponseWriter, r *http.Request, index bleve.Index, conf *config.Config) {
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
	format := r.URL.Query().Get("format")     // "json" (default) or "text"

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

	params := search.SearchParams{
		Query:    q,
		Lat:      lat,
		Lon:      lon,
		Radius:   radius,
		Limit:    limit,
		From:     from,
		Langs:    langs,
		GeoMode:  conf.GeometryMode,
		Fuzzy:    fuzzy,
		Prefix:   prefix,
		Class:    class,
		Subtype:  subtype,
		Classes:  classList,
		Subtypes: subtypeList,
	}

	res, err := search.Search(index, params)
	if err != nil {
		slog.Error("search failed", "error", err, "query", q)
		writeJSONError(w, http.StatusInternalServerError, "search_error", "Search failed: "+err.Error())
		return
	}

	// Support text/plain output format (UNIX-pipe-friendly)
	if format == "text" || strings.Contains(r.Header.Get("Accept"), "text/plain") {
		writeTextResponse(w, res, langs)
	} else {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(res); err != nil {
			slog.Error("failed to encode JSON response", "error", err)
		}
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
	}

	// Summary
	fmt.Fprintf(w, "\n---\ntotal: %d\nreturned: %d\n", res.Total, len(res.Hits))
}

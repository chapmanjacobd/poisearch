package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

func RegisterHandlers(mux *http.ServeMux, index bleve.Index, conf *config.Config) {
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		latStr := r.URL.Query().Get("lat")
		lonStr := r.URL.Query().Get("lon")
		radius := r.URL.Query().Get("radius")
		limitStr := r.URL.Query().Get("limit")
		langsStr := r.URL.Query().Get("langs")
		fuzzy := r.URL.Query().Get("fuzzy") == "1" || r.URL.Query().Get("fuzzy") == "true"
		prefix := r.URL.Query().Get("prefix") == "1" || r.URL.Query().Get("prefix") == "true"
		class := r.URL.Query().Get("class")
		subtype := r.URL.Query().Get("subtype")

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

		var langs []string
		if langsStr != "" {
			langs = strings.Split(langsStr, ",")
		} else {
			langs = conf.Languages
		}

		params := search.SearchParams{
			Query:   q,
			Lat:     lat,
			Lon:     lon,
			Radius:  radius,
			Limit:   limit,
			Langs:   langs,
			GeoMode: conf.GeometryMode,
			Fuzzy:   fuzzy,
			Prefix:  prefix,
			Class:   class,
			Subtype: subtype,
		}

		res, err := search.Search(index, params)
		if err != nil {
			slog.Error("search failed", "error", err, "query", q)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	})
}

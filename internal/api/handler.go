package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

func RegisterHandlers(mux *http.ServeMux, index bleve.Index, conf *config.Config) {
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		latStr := r.URL.Query().Get("lat")
		lonStr := r.URL.Query().Get("lon")
		radius := r.URL.Query().Get("radius")
		limitStr := r.URL.Query().Get("limit")
		langsStr := r.URL.Query().Get("langs")

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
		}

		res, err := search.Search(index, params)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	})
}

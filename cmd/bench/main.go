package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

func main() {
	pbf := "liechtenstein-latest.osm.pbf"
	if _, err := os.Stat(pbf); os.IsNotExist(err) {
		log.Fatalf("PBF file %s not found. Please download it first.", pbf)
	}

	conf := &config.Config{
		IndexPath: "bench.bleve",
		Languages: []string{"en"},
		Importance: config.ImportanceWeights{
			Place:    map[string]float64{"city": 5, "town": 4, "village": 3},
			Amenity:  map[string]float64{"restaurant": 2},
			Highway:  map[string]float64{"primary": 1.5},
			Default:  1.0,
			PopBoost: 5,
		},
		SimplificationTol: 0.0001,
	}

	runFullBench(pbf, conf)
}

func runFullBench(pbf string, conf *config.Config) {
	modes := []string{"geopoint", "geoshape-simplified"}

	for _, mode := range modes {
		fmt.Printf("\n--- Mode: %s ---\n", mode)
		conf.GeometryMode = mode
		if mode == "geopoint" {
			conf.IndexPath = "bench_point.bleve"
		} else {
			conf.IndexPath = "bench_shape.bleve"
		}
		os.RemoveAll(conf.IndexPath)

		start := time.Now()
		m := search.BuildIndexMapping(conf.Languages, conf.GeometryMode)
		index, err := search.OpenOrCreateIndex(conf.IndexPath, m)
		if err != nil {
			log.Fatal(err)
		}
		err = osm.BuildIndex(pbf, conf, index)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Build time: %v\n", time.Since(start))

		// Benchmark Search
		lat, lon := 47.14, 9.52 // Vaduz
		radius := "500m"
		
		// BBox roughly equivalent to 500m
		dLat := 0.0045
		dLon := 0.0066
		minLat, maxLat := lat-dLat, lat+dLat
		minLon, maxLon := lon-dLon, lon+dLon

		// 1. Radius Search
		benchmark(index, "Radius", search.SearchParams{
			Lat:     &lat,
			Lon:     &lon,
			Radius:  radius,
			GeoMode: mode,
			Limit:   50,
		})

		// 2. BBox Search
		benchmark(index, "BBox", search.SearchParams{
			MinLat:  &minLat,
			MaxLat:  &maxLat,
			MinLon:  &minLon,
			MaxLon:  &maxLon,
			GeoMode: mode,
			Limit:   50,
		})

		index.Close()
	}
}

func benchmark(index bleve.Index, label string, params search.SearchParams) {
	start := time.Now()
	iterations := 200
	var count int
	
	for i := 0; i < iterations; i++ {
		res, err := search.Search(index, params)
		if err != nil {
			log.Fatalf("Search failed: %v", err)
		}
		count = int(res.Total)
	}
	avg := time.Since(start) / time.Duration(iterations)
	fmt.Printf("%s Search: Avg Latency: %v, Results: %d\n", label, avg, count)
}

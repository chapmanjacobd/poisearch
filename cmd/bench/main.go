package main

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

type BenchmarkResult struct {
	Label   string
	Latency time.Duration
	Results int
}

type ModeResult struct {
	Label     string
	BuildTime time.Duration
	Size      int64
	Searches  []BenchmarkResult
}

func main() {
	pbf := "liechtenstein-latest.osm.pbf"
	if _, err := os.Stat(pbf); os.IsNotExist(err) {
		log.Fatalf("PBF file %s not found. Please download it first.", pbf)
	}

	conf := &config.Config{
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

func getDirSize(path string) int64 {
	var size int64
	err := filepath.Walk(path, func(_ string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0
	}
	return size
}

func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

func runFullBench(pbf string, conf *config.Config) {
	scenarios := []struct {
		Label     string
		Mode      string
		NodesOnly bool
		Lean      bool
	}{
		{"Leanest Mode", "no-geo", true, true},
		{"No Geo", "no-geo", false, false},
		{"Nodes Only", "geopoint", true, false},
		{"Centroids (Simple)", "geopoint-centroid", false, false},
		{"Representative Pts", "geopoint", false, false},
		{"Simplified Shapes", "geoshape-simplified", false, false},
		{"Raw Shapes", "geoshape-full", false, false},
	}

	var modeResults []ModeResult

	lat, lon := 47.14, 9.52 // Vaduz
	radius := "500m"
	dLat := 0.0045
	dLon := 0.0066
	minLat, maxLat := lat-dLat, lat+dLat
	minLon, maxLon := lon-dLon, lon+dLon

	for _, s := range scenarios {
		fmt.Printf("\n--- Scenario: %s ---\n", s.Label)
		conf.GeometryMode = s.Mode
		conf.NodesOnly = s.NodesOnly
		if s.Lean {
			conf.DisableAltNames = true
			conf.DisableClassSubtype = true
			conf.DisableImportance = true
		} else {
			conf.DisableAltNames = false
			conf.DisableClassSubtype = false
			conf.DisableImportance = false
		}
		
		conf.IndexPath = fmt.Sprintf("bench_%s.bleve", s.Label)
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
		buildTime := time.Since(start)
		size := getDirSize(conf.IndexPath)
		fmt.Printf("Build time: %v, Size: %s\n", buildTime, formatSize(size))

		searchScenarios := []struct {
			Label  string
			Params search.SearchParams
		}{
			{"Basic Search", search.SearchParams{Query: "Vaduz", GeoMode: s.Mode, Limit: 50}},
			{"Fuzzy Search", search.SearchParams{Query: "Vadu", Fuzzy: true, GeoMode: s.Mode, Limit: 50}},
			{"Prefix Search", search.SearchParams{Query: "Vad", Prefix: true, GeoMode: s.Mode, Limit: 50}},
		}

		if !conf.DisableClassSubtype {
			searchScenarios = append(searchScenarios,
				struct {
					Label  string
					Params search.SearchParams
				}{"Class Filter", search.SearchParams{Query: "Vaduz", Class: "place", GeoMode: s.Mode, Limit: 50}},
				struct {
					Label  string
					Params search.SearchParams
				}{"Subtype Filter", search.SearchParams{Query: "Vaduz", Subtype: "city", GeoMode: s.Mode, Limit: 50}},
				struct {
					Label  string
					Params search.SearchParams
				}{"Combined (Fuzzy+Class)", search.SearchParams{Query: "Vadu", Fuzzy: true, Class: "place", GeoMode: s.Mode, Limit: 50}},
			)
		}

		if s.Mode != "no-geo" {
			searchScenarios = append(searchScenarios,
				struct {
					Label  string
					Params search.SearchParams
				}{"Radius Search", search.SearchParams{Lat: &lat, Lon: &lon, Radius: radius, GeoMode: s.Mode, Limit: 50}},
				struct {
					Label  string
					Params search.SearchParams
				}{"BBox Search", search.SearchParams{MinLat: &minLat, MaxLat: &maxLat, MinLon: &minLon, MaxLon: &maxLon, GeoMode: s.Mode, Limit: 50}},
			)
		}

		var bResults []BenchmarkResult
		for _, ss := range searchScenarios {
			res := benchmark(index, ss.Label, ss.Params)
			bResults = append(bResults, res)
		}

		modeResults = append(modeResults, ModeResult{
			Label:     s.Label,
			BuildTime: buildTime,
			Size:      size,
			Searches:  bResults,
		})

		index.Close()
	}

	fmt.Println("\n============================================================")
	fmt.Println("INDEX SIZE COMPARISON")
	fmt.Println("============================================================")
	sort.Slice(modeResults, func(i, j int) bool {
		return modeResults[i].Size < modeResults[j].Size
	})
	fmt.Printf("%-20s %-15s %-15s\n", "Scenario", "Index Size", "Build Time")
	fmt.Println("------------------------------------------------------------")
	for _, r := range modeResults {
		fmt.Printf("%-20s %-15s %-15v\n", r.Label, formatSize(r.Size), r.BuildTime)
	}

	fmt.Println("\n============================================================")
	fmt.Println("FULL PERFORMANCE REPORT")
	fmt.Println("============================================================")
	for _, r := range modeResults {
		fmt.Printf("\n--- %s (%s) ---\n", r.Label, formatSize(r.Size))
		sort.Slice(r.Searches, func(i, j int) bool {
			return r.Searches[i].Latency < r.Searches[j].Latency
		})
		for _, s := range r.Searches {
			fmt.Printf("%-25s %-15v %-10d\n", s.Label, s.Latency, s.Results)
		}
	}
}

func benchmark(index bleve.Index, label string, params search.SearchParams) BenchmarkResult {
	start := time.Now()
	iterations := 200
	var count int

	for i := 0; i < iterations; i++ {
		res, err := search.Search(index, params)
		if err != nil {
			log.Fatalf("Search failed for %s: %v", label, err)
		}
		count = int(res.Total)
	}
	avg := time.Since(start) / time.Duration(iterations)
	fmt.Printf("  %-25s Avg: %-10v Results: %d\n", label, avg, count)
	return BenchmarkResult{Label: label, Latency: avg, Results: count}
}

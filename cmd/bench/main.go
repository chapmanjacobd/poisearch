package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

type BenchmarkResult struct {
	ModeLabel string
	Label     string
	Latency   time.Duration
	Results   int
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
			Shop:     map[string]float64{"bakery": 1.2},
			Tourism:  map[string]float64{"museum": 2.5},
			Default:  1.0,
			PopBoost: 0.2,
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
		PBFOnly   bool
	}{
		{"Leanest Mode", "no-geo", true, true, false},
		{"No Geo", "no-geo", false, false, false},
		{"Nodes Only", "geopoint", true, false, false},
		{"Centroids (Simple)", "geopoint-centroid", false, false, false},
		{"Representative Pts", "geopoint", false, false, false},
		{"Simplified Shapes", "geoshape-simplified", false, false, false},
		{"Raw Shapes", "geoshape-full", false, false, false},
		{"Raw PBF Scan", "no-geo", false, false, true},
	}

	var modeResults []ModeResult
	var allSearchResults []BenchmarkResult

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
		conf.StoreMetadata = true
		conf.StoreGeometry = true
		conf.OnlyNamed = false

		if s.Lean {
			conf.DisableAltNames = true
			conf.DisableClassSubtype = true
			conf.DisableImportance = true
			conf.OnlyNamed = true
			conf.StoreMetadata = false
			conf.StoreGeometry = false
		} else {
			conf.DisableAltNames = false
			conf.DisableClassSubtype = false
			conf.DisableImportance = false
		}
		
		var index bleve.Index
		var buildTime time.Duration
		var size int64

		if !s.PBFOnly {
			conf.IndexPath = fmt.Sprintf("bench_%s.bleve", s.Label)
			os.RemoveAll(conf.IndexPath)

			start := time.Now()
			m := search.BuildIndexMapping(conf)
			idx, err := search.OpenOrCreateIndex(conf.IndexPath, m)
			if err != nil {
				log.Fatal(err)
			}
			index = idx
			err = osm.BuildIndex(pbf, conf, index)
			if err != nil {
				log.Fatal(err)
			}
			buildTime = time.Since(start)
			size = getDirSize(conf.IndexPath)
			fmt.Printf("Build time: %v, Size: %s\n", buildTime, formatSize(size))
		} else {
			fmt.Printf("PBF Only: No build needed. Using source: %s\n", pbf)
			buildTime = 0
			size = 0
		}

		searchScenarios := []struct {
			Label  string
			Params search.SearchParams
		}{
			{"Basic Search", search.SearchParams{Query: "Vaduz", GeoMode: s.Mode, Limit: 50}},
			{"Fuzzy Search", search.SearchParams{Query: "Vadu", Fuzzy: true, GeoMode: s.Mode, Limit: 50}},
			{"Prefix Search", search.SearchParams{Query: "vad", Prefix: true, GeoMode: s.Mode, Limit: 50}},
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
				}{"Subtype Filter", search.SearchParams{Query: "Vaduz", Subtype: "town", GeoMode: s.Mode, Limit: 50}},
				struct {
					Label  string
					Params search.SearchParams
				}{"Combined (Fuzzy+Class)", search.SearchParams{Query: "Vadu", Fuzzy: true, Class: "place", GeoMode: s.Mode, Limit: 50}},
				struct {
					Label  string
					Params search.SearchParams
				}{"Shop Search", search.SearchParams{Subtype: "bakery", GeoMode: s.Mode, Limit: 50}},
				struct {
					Label  string
					Params search.SearchParams
				}{"Tourism Search", search.SearchParams{Subtype: "museum", GeoMode: s.Mode, Limit: 50}},
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
			var res BenchmarkResult
			if s.PBFOnly {
				res = benchmarkPBF(pbf, ss.Label, ss.Params, conf)
			} else {
				res = benchmark(index, ss.Label, ss.Params)
			}
			res.ModeLabel = s.Label
			bResults = append(bResults, res)
			allSearchResults = append(allSearchResults, res)
		}

		modeResults = append(modeResults, ModeResult{
			Label:     s.Label,
			BuildTime: buildTime,
			Size:      size,
			Searches:  bResults,
		})

		if index != nil {
			index.Close()
		}
	}

	buf := new(bytes.Buffer)
	w := io.MultiWriter(os.Stdout, buf)

	fmt.Fprintln(w, "\n============================================================")
	fmt.Fprintln(w, "INDEX SIZE COMPARISON")
	fmt.Fprintln(w, "============================================================")
	sort.Slice(modeResults, func(i, j int) bool {
		return modeResults[i].Size < modeResults[j].Size
	})
	fmt.Fprintf(w, "%-20s %-15s %-15s\n", "Scenario", "Index Size", "Build Time")
	fmt.Fprintln(w, "------------------------------------------------------------")
	for _, r := range modeResults {
		sizeStr := formatSize(r.Size)
		if r.Label == "Raw PBF Scan" {
			sizeStr = "0 B (Live)"
		}
		fmt.Fprintf(w, "%-20s %-15s %-15v\n", r.Label, sizeStr, r.BuildTime)
	}

	fmt.Fprintln(w, "\n============================================================")
	fmt.Fprintln(w, "FULL PERFORMANCE REPORT (Sorted by Latency)")
	fmt.Fprintln(w, "============================================================")
	sort.Slice(allSearchResults, func(i, j int) bool {
		return allSearchResults[i].Latency < allSearchResults[j].Latency
	})
	fmt.Fprintf(w, "%-20s %-25s %-15s %-10s\n", "Spatial Mode", "Scenario", "Avg Latency", "Results")
	fmt.Fprintln(w, "--------------------------------------------------------------------------------")
	for _, r := range allSearchResults {
		fmt.Fprintf(w, "%-20s %-25s %-15v %-10d\n", r.ModeLabel, r.Label, r.Latency, r.Results)
	}

	updateReadme(buf.String())
}

func updateReadme(report string) {
	const readmeFile = "README.md"
	content, err := os.ReadFile(readmeFile)
	if err != nil {
		log.Printf("failed to read README.md: %v", err)
		return
	}

	lines := strings.Split(string(content), "\n")
	newLines := []string{}
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, "```plain") {
			for j := i; j < len(lines); j++ {
				if strings.HasPrefix(lines[j], "```") && j > i {
					found = true
					newLines = append(newLines, "```plain")
					newLines = append(newLines, strings.TrimSpace(report))
					newLines = append(newLines, "```")
					lines = lines[j+1:]
					break
				}
			}
			if found {
				break
			}
		}
		newLines = append(newLines, line)
	}

	if !found {
		newLines = append(newLines, "\n## Benchmark Results\n")
		newLines = append(newLines, "```plain")
		newLines = append(newLines, strings.TrimSpace(report))
		newLines = append(newLines, "```")
	} else {
		newLines = append(newLines, lines...)
	}

	err = os.WriteFile(readmeFile, []byte(strings.Join(newLines, "\n")), 0644)
	if err != nil {
		log.Printf("failed to write README.md: %v", err)
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

func benchmarkPBF(pbfPath string, label string, params search.SearchParams, conf *config.Config) BenchmarkResult {
	start := time.Now()
	iterations := 5
	var count int

	for i := 0; i < iterations; i++ {
		res, err := osm.PBFSearch(pbfPath, params, conf)
		if err != nil {
			log.Fatalf("PBF search failed for %s: %v", label, err)
		}
		count = int(res.Total)
	}
	avg := time.Since(start) / time.Duration(iterations)
	fmt.Printf("  %-25s Avg: %-10v Results: %d\n", label, avg, count)
	return BenchmarkResult{Label: label, Latency: avg, Results: count}
}

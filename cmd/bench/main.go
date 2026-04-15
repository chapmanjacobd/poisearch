package main

import (
	"bytes"
	"flag"
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
	slow := flag.Bool("slow", false, "Use larger Taiwan PBF for benchmarking")
	flag.Parse()

	conf := &config.Config{
		Languages: []string{"en"},
		Importance: config.ImportanceWeights{
			Boosts: []string{
				"city",
				"town",
				"village",
				"museum",
				"restaurant",
				"primary",
				"bakery",
			},
			Default:  1.0,
			PopBoost: 0.2,
		},
		SimplificationTol: 0.0001,
		PBFPath:           config.DefaultPBF,
		Server:            config.ServerConfig{Host: "127.0.0.1", Port: config.DefaultPort},
	}

	// Check if PBF file exists
	pbf := conf.PBFPath
	if *slow {
		pbf = "taiwan-latest.osm.pbf"
		fmt.Printf("Using Taiwan PBF for benchmarking: %s\n", pbf)
	}

	if _, err := os.Stat(pbf); os.IsNotExist(err) {
		log.Fatalf("PBF file %s not found. Please download it first or set pbf_path in config.", pbf)
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

func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	if d >= time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	if d >= time.Millisecond {
		return fmt.Sprintf("%.2fms", float64(d.Nanoseconds())/1e6)
	}
	if d >= time.Microsecond {
		return fmt.Sprintf("%.2fµs", float64(d.Nanoseconds())/1e3)
	}
	return d.String()
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

// runFullBench runs comprehensive benchmarks across multiple geometry modes and search scenarios.
//
//nolint:funlen,revive // Benchmark function needs to be comprehensive to cover all scenarios
func runFullBench(pbf string, conf *config.Config) {
	// First run analyzer comparison benchmark
	runAnalyzerBench(pbf, conf)

	// Then run the full geometry mode benchmark
	scenarios := []struct {
		Label         string
		Mode          string
		NodesOnly     bool
		Minimal       bool
		PBFOnly       bool
		PMTilesOnly   bool
		StoreAddress  bool
		WikiRedirects bool
		CacheEnabled  bool
	}{
		{"Minimal Mode", "no-geo", true, true, false, false, false, false, false},
		{"No Geo", "no-geo", false, false, false, false, false, false, false},
		{"Nodes Only", "geopoint", true, false, false, false, false, false, false},
		{"Centroids (Simple)", "geopoint-centroid", false, false, false, false, false, false, false},
		{"Representative Pts", "geopoint", false, false, false, false, false, false, false},
		{"Bounding Boxes", "geoshape-bbox", false, false, false, false, false, false, false},
		{"PBF Scan", "no-geo", false, false, true, false, false, false, false},
		{"PMTiles Scan", "geopoint", false, false, false, true, false, false, false},
		{"Addresses", "geopoint-centroid", false, false, false, false, true, false, false},
		{"Wiki Redirects", "geopoint-centroid", false, false, false, false, false, true, false},
		// {"Cached Searches", "geopoint-centroid", false, false, false, false, false, false, true},
	}

	modeResults := make([]ModeResult, 0, len(scenarios))
	var allSearchResults []BenchmarkResult

	lat, lon := 47.14, 9.52 // Vaduz
	city := "Vaduz"
	value := "town"
	pmtiles := "liechtenstein.pmtiles"
	if pbf == "taiwan-latest.osm.pbf" {
		lat, lon = 25.03, 121.56 // Taipei
		city = "Taipei"
		value = "city"
	}
	radius := "1000m"
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
		conf.StoreAddress = s.StoreAddress
		conf.IndexWikidataRedirects = s.WikiRedirects
		conf.CacheEnabled = s.CacheEnabled

		if s.Minimal {
			conf.DisableAltNames = true
			conf.DisableKeyValues = true
			conf.DisableImportance = true
			conf.OnlyNamed = true
			conf.StoreMetadata = false
			conf.StoreGeometry = false
		} else {
			conf.DisableAltNames = false
			conf.DisableKeyValues = false
			conf.DisableImportance = false
		}

		var index bleve.Index
		var buildTime time.Duration
		var size int64

		switch {
		case !s.PBFOnly && !s.PMTilesOnly:
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
			fmt.Printf("Build time: %s, Size: %s\n", formatDuration(buildTime), formatSize(size))
		case s.PBFOnly:
			fmt.Printf("PBF Only: No build needed. Using source: %s\n", pbf)
			buildTime = 0
			size = 0
		case s.PMTilesOnly:
			fmt.Printf("PMTiles Only: No build needed. Using source: %s\n", pmtiles)
			buildTime = 0
			size = 0
		}

		searchScenarios := []struct {
			Label  string
			Params search.SearchParams
		}{
			{"Basic Search", search.SearchParams{Query: city, GeoMode: s.Mode, Limit: 50}},
			{"Fuzzy Search", search.SearchParams{Query: city[:len(city)-1], Fuzzy: true, GeoMode: s.Mode, Limit: 50}},
			{
				"Prefix Search",
				search.SearchParams{Query: strings.ToLower(city[:3]), Prefix: true, GeoMode: s.Mode, Limit: 50},
			},
		}

		if !conf.DisableKeyValues {
			searchScenarios = append(searchScenarios,
				struct {
					Label  string
					Params search.SearchParams
				}{"Key Filter", search.SearchParams{Query: city, Key: "place", GeoMode: s.Mode, Limit: 50}},
				struct {
					Label  string
					Params search.SearchParams
				}{"Value Filter", search.SearchParams{Query: city, Value: value, GeoMode: s.Mode, Limit: 50}},
				struct {
					Label  string
					Params search.SearchParams
				}{"Combined (Fuzzy+Key)", search.SearchParams{Query: city[:len(city)-1], Fuzzy: true, Key: "place", GeoMode: s.Mode, Limit: 50}},
				struct {
					Label  string
					Params search.SearchParams
				}{"Shop Search", search.SearchParams{Value: "bakery", GeoMode: s.Mode, Limit: 50}},
				struct {
					Label  string
					Params search.SearchParams
				}{"Tourism Search", search.SearchParams{Value: "museum", GeoMode: s.Mode, Limit: 50}},
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

		if s.StoreAddress {
			street := "Herrengasse"
			if city == "Taipei" {
				street = "Xinyi Road"
			}
			searchScenarios = append(searchScenarios,
				struct {
					Label  string
					Params search.SearchParams
				}{"Address Match", search.SearchParams{Street: street, City: city, GeoMode: s.Mode, Limit: 50}},
			)
		}

		var bResults []BenchmarkResult
		for _, ss := range searchScenarios {
			var res BenchmarkResult
			// Inject spatial filter for PMTiles if missing, as it's required
			if s.PMTilesOnly && ss.Params.Lat == nil && ss.Params.MinLat == nil {
				ss.Params.Lat = &lat
				ss.Params.Lon = &lon
				ss.Params.Radius = radius
			}

			switch {
			case s.PBFOnly:
				res = benchmarkPBF(pbf, ss.Label, ss.Params, conf)
			case s.PMTilesOnly:
				res = benchmarkPMTiles(pmtiles, ss.Label, ss.Params, conf)
			default:
				res = benchmark(index, ss.Label, ss.Params, conf)
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
		if r.Label == "PBF Scan" || r.Label == "PMTiles Scan" {
			sizeStr = "0 B (Live)"
		}
		fmt.Fprintf(w, "%-20s %-15s %-15s\n", r.Label, sizeStr, formatDuration(r.BuildTime))
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
		fmt.Fprintf(w, "%-20s %-25s %-15s %-10d\n", r.ModeLabel, r.Label, formatDuration(r.Latency), r.Results)
	}

	title := fmt.Sprintf("Benchmark Results (%s, %s)", filepath.Base(pbf), formatSize(getDirSize(pbf)))
	updateReadme(title, buf.String())
}

func benchmarkPMTiles(pmtilesPath, label string, params search.SearchParams, conf *config.Config) BenchmarkResult {
	start := time.Now()
	iterations := 5
	var count int

	for range iterations {
		res, err := osm.PMTilesSearch(pmtilesPath, params, conf)
		if err != nil {
			// Skip if it fails (e.g. no spatial filter)
			continue
		}
		count = int(res.Total)
	}
	avg := time.Since(start) / time.Duration(iterations)
	fmt.Printf("  %-25s Avg: %-10s Results: %d\n", label, formatDuration(avg), count)
	return BenchmarkResult{Label: label, Latency: avg, Results: count}
}

func updateReadme(title, report string) {
	const readmeFile = "README.md"
	content, err := os.ReadFile(readmeFile)
	if err != nil {
		log.Printf("failed to read README.md: %v", err)
		return
	}

	lines := strings.Split(string(content), "\n")
	newLines, updated := replaceReadmeSection(lines, title, report)

	if !updated {
		newLines = append(newLines, "## "+title)
		newLines = append(newLines, "```plain")
		newLines = append(newLines, strings.TrimSpace(report))
		newLines = append(newLines, "```")
	}

	err = os.WriteFile(readmeFile, []byte(strings.Join(newLines, "\n")), 0o644)
	if err != nil {
		log.Printf("failed to write README.md: %v", err)
	}
}

func replaceReadmeSection(lines []string, title, report string) ([]string, bool) {
	newLines := []string{}
	updated := false
	i := 0
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "## "+title) && !updated {
			updated = true
			newLines = append(newLines, line)
			i = skipOldBlock(lines, i+1, &newLines, report)
		} else {
			newLines = append(newLines, line)
			i++
		}
	}
	return newLines, updated
}

func skipOldBlock(lines []string, start int, newLines *[]string, report string) int {
	foundCodeBlock := false
	codeBlockEnd := -1

	for j := start; j < len(lines); j++ {
		if strings.HasPrefix(lines[j], "```plain") {
			for k := j + 1; k < len(lines); k++ {
				if strings.HasPrefix(lines[k], "```") {
					codeBlockEnd = k
					break
				}
			}
			foundCodeBlock = true
			break
		}
		if strings.HasPrefix(lines[j], "## ") {
			break
		}
	}

	*newLines = append(*newLines, "```plain")
	*newLines = append(*newLines, strings.TrimSpace(report))
	*newLines = append(*newLines, "```")

	if foundCodeBlock {
		return codeBlockEnd + 1
	}
	return start
}

// runAnalyzerBench runs analyzer comparison benchmarks to measure search performance.
//
//nolint:funlen,revive // Benchmark function needs to compare multiple analyzers comprehensively
func runAnalyzerBench(pbf string, conf *config.Config) {
	fmt.Println("\n============================================================")
	fmt.Println("ANALYZER COMPARISON BENCHMARK")
	fmt.Println("============================================================")

	analyzers := []string{"standard", "edge_ngram", "ngram"}

	type AnalyzerResult struct {
		Name      string
		BuildTime time.Duration
		Size      int64
		Searches  []BenchmarkResult
	}

	analyzerResults := make([]AnalyzerResult, 0, len(analyzers))

	lat, lon := 47.14, 9.52 // Vaduz
	city := "Vaduz"
	if pbf == "taiwan-latest.osm.pbf" {
		lat, lon = 25.03, 121.56 // Taipei
		city = "Taipei"
	}

	searchQueries := []struct {
		Label  string
		Params search.SearchParams
	}{
		{"Exact: " + city, search.SearchParams{Query: city, Limit: 50}},
		{"Prefix: " + strings.ToLower(city[:3]), search.SearchParams{Query: strings.ToLower(city[:3]), Limit: 50}},
		{"Partial: " + city[:len(city)-1], search.SearchParams{Query: city[:len(city)-1], Limit: 50}},
		{"Autocomplete: rest", search.SearchParams{Query: "rest", Limit: 50}},
		{"Short: " + strings.ToLower(city[:2]), search.SearchParams{Query: strings.ToLower(city[:2]), Limit: 50}},
		{"Geo + Text", search.SearchParams{Query: city, Lat: &lat, Lon: &lon, Radius: "1000m", Limit: 50}},
	}

	for _, analyzer := range analyzers {
		fmt.Printf("\n--- Analyzer: %s ---\n", analyzer)

		testConf := *conf
		testConf.NameAnalyzer = analyzer
		testConf.IndexPath = fmt.Sprintf("bench_analyzer_%s.bleve", analyzer)
		testConf.GeometryMode = "geopoint-centroid"
		testConf.StoreMetadata = false
		testConf.StoreGeometry = false
		os.RemoveAll(testConf.IndexPath)

		start := time.Now()
		m := search.BuildIndexMapping(&testConf)
		idx, err := search.OpenOrCreateIndex(testConf.IndexPath, m)
		if err != nil {
			log.Fatal(err)
		}
		err = osm.BuildIndex(pbf, &testConf, idx)
		if err != nil {
			log.Fatal(err)
		}
		buildTime := time.Since(start)
		size := getDirSize(testConf.IndexPath)
		fmt.Printf("Build time: %s, Size: %s\n", formatDuration(buildTime), formatSize(size))

		searchResults := make([]BenchmarkResult, 0, len(searchQueries))
		for _, sq := range searchQueries {
			sq.Params.GeoMode = testConf.GeometryMode
			sq.Params.Analyzer = analyzer
			res := benchmark(idx, sq.Label, sq.Params, &testConf)
			res.ModeLabel = analyzer
			searchResults = append(searchResults, res)
		}

		analyzerResults = append(analyzerResults, AnalyzerResult{
			Name:      analyzer,
			BuildTime: buildTime,
			Size:      size,
			Searches:  searchResults,
		})

		idx.Close()
	}

	// Print comparison table
	fmt.Println("\n============================================================")
	fmt.Println("ANALYZER SIZE COMPARISON")
	fmt.Println("============================================================")
	fmt.Fprintf(os.Stdout, "%-18s %-15s %-15s\n", "Analyzer", "Index Size", "Build Time")
	fmt.Println("------------------------------------------------------------")
	for _, r := range analyzerResults {
		fmt.Fprintf(os.Stdout, "%-18s %-15s %-15s\n", r.Name, formatSize(r.Size), formatDuration(r.BuildTime))
	}

	fmt.Println("\n============================================================")
	fmt.Println("ANALYZER SEARCH LATENCY (sorted by average)")
	fmt.Println("============================================================")

	// Collect all search results and compute average per analyzer
	type AnalyzerAvg struct {
		Name    string
		Avg     time.Duration
		Min     time.Duration
		Max     time.Duration
		Queries int
	}

	avgs := make([]AnalyzerAvg, 0, len(analyzerResults))
	for _, ar := range analyzerResults {
		var total time.Duration
		minLat := ar.Searches[0].Latency
		maxLat := ar.Searches[0].Latency
		for _, s := range ar.Searches {
			total += s.Latency
			if s.Latency < minLat {
				minLat = s.Latency
			}
			if s.Latency > maxLat {
				maxLat = s.Latency
			}
		}
		avg := total / time.Duration(len(ar.Searches))
		avgs = append(avgs, AnalyzerAvg{
			Name:    ar.Name,
			Avg:     avg,
			Min:     minLat,
			Max:     maxLat,
			Queries: len(ar.Searches),
		})
	}

	sort.Slice(avgs, func(i, j int) bool {
		return avgs[i].Avg < avgs[j].Avg
	})

	fmt.Fprintf(
		os.Stdout,
		"%-18s %-15s %-15s %-15s %-10s\n",
		"Analyzer",
		"Avg Latency",
		"Min Latency",
		"Max Latency",
		"Queries",
	)
	fmt.Println("------------------------------------------------------------------------------------")
	for _, a := range avgs {
		fmt.Fprintf(
			os.Stdout,
			"%-18s %-15s %-15s %-15s %-10d\n",
			a.Name,
			formatDuration(a.Avg),
			formatDuration(a.Min),
			formatDuration(a.Max),
			a.Queries,
		)
	}

	// Detailed per-query comparison
	fmt.Println("\n============================================================")
	fmt.Println("DETAILED PER-QUERY COMPARISON")
	fmt.Println("============================================================")
	for i, sq := range searchQueries {
		fmt.Printf("\n--- %s ---\n", sq.Label)
		fmt.Fprintf(os.Stdout, "%-18s %-15s %-10s\n", "Analyzer", "Latency", "Results")
		fmt.Println("----------------------------------------")
		for _, ar := range analyzerResults {
			if i < len(ar.Searches) {
				sr := ar.Searches[i]
				fmt.Fprintf(os.Stdout, "%-18s %-15s %-10d\n", ar.Name, formatDuration(sr.Latency), sr.Results)
			}
		}
	}
}

func benchmark(index bleve.Index, label string, params search.SearchParams, conf *config.Config) BenchmarkResult {
	start := time.Now()
	iterations := 200
	var count int

	var cache *search.QueryCache
	if conf.CacheEnabled {
		// Fresh cache for each benchmark query type to measure first-miss-then-hit avg
		cache, _ = search.NewQueryCache(config.DefaultCacheSize, config.DefaultCacheTTL)
	}

	for range iterations {
		if cache != nil {
			key := search.BuildCacheKey(params)
			if cached, ok := cache.Get(key); ok {
				count = int(cached.Total)
				continue
			}
			res, err := search.Search(index, params)
			if err != nil {
				log.Fatalf("Search failed for %s: %v", label, err)
			}
			cache.Set(key, search.SerializeResult(res))
			count = int(res.Total)
		} else {
			res, err := search.Search(index, params)
			if err != nil {
				log.Fatalf("Search failed for %s: %v", label, err)
			}
			count = int(res.Total)
		}
	}
	avg := time.Since(start) / time.Duration(iterations)
	fmt.Printf("  %-25s Avg: %-10s Results: %d\n", label, formatDuration(avg), count)
	return BenchmarkResult{Label: label, Latency: avg, Results: count}
}

func benchmarkPBF(pbfPath, label string, params search.SearchParams, conf *config.Config) BenchmarkResult {
	start := time.Now()
	iterations := 5
	var count int

	for range iterations {
		res, err := osm.PBFSearch(pbfPath, params, conf)
		if err != nil {
			log.Fatalf("PBF search failed for %s: %v", label, err)
		}
		count = int(res.Total)
	}
	avg := time.Since(start) / time.Duration(iterations)
	fmt.Printf("  %-25s Avg: %-10s Results: %d\n", label, formatDuration(avg), count)
	return BenchmarkResult{Label: label, Latency: avg, Results: count}
}

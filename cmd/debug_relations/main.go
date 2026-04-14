package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"

	"github.com/chapmanjacobd/poisearch/internal/config"
	"github.com/chapmanjacobd/poisearch/internal/osm"
	osmapi "github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: debug_relations <pbf_file>")
		os.Exit(1)
	}
	pbfPath := os.Args[1]

	f, err := os.Open(pbfPath)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	scanner := osmpbf.New(context.Background(), f, runtime.GOMAXPROCS(-1))
	defer scanner.Close()

	conf := config.Config{
		Importance: config.ImportanceWeights{
			Default: 1.0,
		},
	}
	ont := osm.DefaultOntology()

	type stats struct {
		count          int
		uniqueNames    int // Relations with a name/ref not likely on nodes
		totalMembers   int
		routeTypes     map[string]int
	}

	routeStats := &stats{routeTypes: make(map[string]int)}
	boundaryStats := &stats{routeTypes: make(map[string]int)}
	otherStats := &stats{routeTypes: make(map[string]int)}

	fmt.Printf("Analyzing unique value of relations in %s...\n", pbfPath)

	for scanner.Scan() {
		obj := scanner.Object()
		rel, ok := obj.(*osmapi.Relation)
		if !ok {
			continue
		}

		tags := rel.TagMap()
		if len(tags) == 0 {
			continue
		}

		classifications := osm.ClassifyMulti(tags, &conf.Importance, ont)
		if len(classifications) == 0 {
			continue
		}

		// Determine category
		var s *stats
		if tags["route"] != "" {
			s = routeStats
			routeStats.routeTypes[tags["route"]]++
		} else if tags["boundary"] == "administrative" {
			s = boundaryStats
		} else {
			s = otherStats
		}

		s.count++
		s.totalMembers += len(rel.Members)

		name := tags["name"]
		ref := tags["ref"]
		
		// Heuristic for "unique value":
		// Transit routes usually have a 'ref' (line number) which is rarely on the stops themselves
		// (Stops are named "Main St", routes are "Line 513")
		if ref != "" || (name != "" && !strings.Contains(strings.ToLower(name), "stop")) {
			s.uniqueNames++
		}

		// Print first few transit routes for inspection
		if tags["route"] != "" && routeStats.count <= 10 {
			fmt.Printf("\nTransit Relation %d (%s):\n", rel.ID, tags["route"])
			fmt.Printf("  Name: %s\n", name)
			fmt.Printf("  Ref:  %s\n", ref)
			fmt.Printf("  Operator: %s\n", tags["operator"])
			fmt.Printf("  Search terms: If I search '%s', will I find any nodes?\n", ref)
		}
	}

	fmt.Printf("\n--- Route Relations (Transit, etc) ---\n")
	fmt.Printf("  Count: %d\n", routeStats.count)
	fmt.Printf("  With Unique Name/Ref: %d\n", routeStats.uniqueNames)
	fmt.Printf("  Avg Members: %.1f\n", float64(routeStats.totalMembers)/float64(routeStats.count))
	fmt.Printf("  Types: %v\n", routeStats.routeTypes)

	fmt.Printf("\n--- Boundary Relations (Admin) ---\n")
	fmt.Printf("  Count: %d\n", boundaryStats.count)
	fmt.Printf("  With Unique Name: %d\n", boundaryStats.uniqueNames)

	fmt.Printf("\n--- Other POI Relations ---\n")
	fmt.Printf("  Count: %d\n", otherStats.count)
	fmt.Printf("  With Unique Name: %d\n", otherStats.uniqueNames)
}

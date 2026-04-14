package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"

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

	unresolvable := 0
	resolvableWithWays := 0
	totalRelations := 0
	taggedRelations := 0

	fmt.Printf("Analyzing relations in %s...\n", pbfPath)

	for scanner.Scan() {
		obj := scanner.Object()
		if rel, ok := obj.(*osmapi.Relation); ok {
			totalRelations++
			tags := rel.TagMap()
			if len(tags) == 0 {
				continue
			}

			// We only care about relations that would actually be indexed (ClassifyMulti returns something)
			classifications := osm.ClassifyMulti(tags, &conf.Importance, ont)
			if len(classifications) == 0 {
				continue
			}

			taggedRelations++

			hasWayLocation := false
			hasNodeMember := false
			for _, member := range rel.Members {
				if member.Type == osmapi.TypeWay {
					if member.Lat != 0 || member.Lon != 0 {
						hasWayLocation = true
						break
					}
				}
				if member.Type == osmapi.TypeNode {
					hasNodeMember = true
				}
			}

			if hasWayLocation {
				resolvableWithWays++
			} else if hasNodeMember {
				unresolvable++
				if unresolvable <= 20 {
					fmt.Printf("\nUnresolvable Relation %d:\n", rel.ID)
					fmt.Printf("  Tags: %v\n", tags)
					fmt.Printf("  Members: %d total\n", len(rel.Members))
					nodeCount := 0
					wayCount := 0
					for _, m := range rel.Members {
						if m.Type == osmapi.TypeNode {
							nodeCount++
						} else if m.Type == osmapi.TypeWay {
							wayCount++
						}
					}
					fmt.Printf("  Nodes: %d, Ways: %d\n", nodeCount, wayCount)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\nSummary:\n")
	fmt.Printf("Total Relations:      %d\n", totalRelations)
	fmt.Printf("Tagged/POI Relations: %d\n", taggedRelations)
	fmt.Printf("Resolvable via Ways:  %d\n", resolvableWithWays)
	fmt.Printf("Unresolvable (Nodes): %d\n", unresolvable)
}

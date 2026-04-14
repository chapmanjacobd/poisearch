package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/maptile"
	"github.com/protomaps/go-pmtiles/pmtiles"
)

func main() {
	// Command line flags
	pmtilesPath := flag.String("file", "", "Path to .pmtiles file")
	lat := flag.Float64("lat", 25.03, "Latitude to query")
	lon := flag.Float64("lon", 121.56, "Longitude to query")
	zoom := flag.Int("zoom", -1, "Specific zoom level to query (-1 for all)")
	layerName := flag.String("layer", "", "Filter to specific layer name")
	maxFeatures := flag.Int("max-features", 10, "Max features to show per layer")
	listLayers := flag.Bool("list-layers", false, "Only list available layers")
	flag.Parse()

	if *pmtilesPath == "" {
		fmt.Println("Usage: debug_pmtiles -file <path> [options]")
		fmt.Println("Options:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	ctx := context.Background()
	dir := filepath.Dir(*pmtilesPath)
	filename := filepath.Base(*pmtilesPath)
	bucket := pmtiles.NewFileBucket(dir)
	defer bucket.Close()

	// Read header
	r, err := bucket.NewRangeReader(ctx, filename, 0, pmtiles.HeaderV3LenBytes)
	if err != nil {
		log.Fatalf("reading header: %v", err)
	}
	headerBytes, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		log.Fatalf("reading header bytes: %v", err)
	}

	header, err := pmtiles.DeserializeHeader(headerBytes)
	if err != nil {
		log.Fatalf("deserializing header: %v", err)
	}

	fmt.Printf("File: %s\n", filename)
	fmt.Printf("MinZoom: %d, MaxZoom: %d\n", header.MinZoom, header.MaxZoom)
	fmt.Printf("Center: (%f, %f) @ zoom %d\n", header.CenterLonE7/1e7, header.CenterLatE7/1e7, header.CenterZoom)

	// Determine zoom range to query
	startZoom := int(header.MinZoom)
	endZoom := int(header.MaxZoom)
	if *zoom >= 0 {
		if uint8(*zoom) < header.MinZoom || uint8(*zoom) > header.MaxZoom {
			log.Fatalf("Requested zoom %d is outside range [%d, %d]", *zoom, header.MinZoom, header.MaxZoom)
		}
		startZoom = *zoom
		endZoom = *zoom
	}

	fmt.Printf("\nQuerying point: lat=%f, lon=%f\n", *lat, *lon)

	for z := startZoom; z <= endZoom; z++ {
		x, y := lonLatToTile(*lon, *lat, z)
		fmt.Printf("\n=== Zoom Level %d (Tile %d/%d) ===\n", z, x, y)

		tileData, err := readTileDirect(ctx, bucket, filename, header, uint8(z), uint32(x), uint32(y))
		if err != nil {
			fmt.Printf("  Tile read error: %v\n", err)
			continue
		}
		if tileData == nil {
			fmt.Printf("  Tile not available\n")
			continue
		}

		layers, err := mvt.Unmarshal(tileData)
		if err != nil {
			// Try gzipped
			layers, err = mvt.UnmarshalGzipped(tileData)
			if err != nil {
				fmt.Printf("  Failed to unmarshal MVT: %v\n", err)
				continue
			}
		}

		tile := maptile.New(uint32(x), uint32(y), maptile.Zoom(z))

		// Collect layer names for listing mode
		var layerNames []string

		for _, layer := range layers {
			layerNames = append(layerNames, layer.Name)

			// Skip if filtering by layer name
			if *layerName != "" && layer.Name != *layerName {
				continue
			}

			layer.ProjectToWGS84(tile)

			fmt.Printf("\n  Layer: %s (%d features)\n", layer.Name, len(layer.Features))

			// Collect and display property keys from first few features
			propertyKeys := make(map[string]bool)
			sampleSize := min(*maxFeatures, len(layer.Features))

			for _, f := range layer.Features[:sampleSize] {
				for k := range f.Properties {
					propertyKeys[k] = true
				}
			}

			// Sort and display property keys
			var keys []string
			for k := range propertyKeys {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Printf("    Properties: %s\n", strings.Join(keys, ", "))

			// Display features
			for i, f := range layer.Features {
				if i >= *maxFeatures {
					fmt.Printf("    ... and %d more features\n", len(layer.Features)-*maxFeatures)
					break
				}

				// Format geometry summary
				geomSummary := formatGeometrySummary(f.Geometry)

				// Create a compact properties map
				props := make(map[string]any)
				for k, v := range f.Properties {
					// Skip very long values for display
					if s, ok := v.(string); ok && len(s) > 100 {
						props[k] = s[:100] + "..."
					} else {
						props[k] = v
					}
				}

				propsJSON, _ := json.Marshal(props)

				fmt.Printf("    [%d] ID: %v\n", i, f.ID)
				fmt.Printf("        Geometry: %s\n", geomSummary)
				fmt.Printf("        Properties: %s\n", string(propsJSON))
			}
		}

		// List layers mode - exit after first successful tile
		if *listLayers {
			fmt.Printf("\nAvailable layers: %s\n", strings.Join(layerNames, ", "))
			return
		}
	}
}

// readTileDirect reads a tile directly from the PMTiles file without using a server.
// Based on the approach from pmtiles/show.go
func readTileDirect(
	ctx context.Context,
	bucket *pmtiles.FileBucket,
	filename string,
	header pmtiles.HeaderV3,
	z uint8,
	x, y uint32,
) ([]byte, error) {
	tileID := pmtiles.ZxyToID(z, x, y)
	dirOffset := header.RootOffset
	dirLength := header.RootLength

	for range 4 {
		r, err := bucket.NewRangeReader(ctx, filename, int64(dirOffset), int64(dirLength))
		if err != nil {
			return nil, fmt.Errorf("reading directory: %w", err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			return nil, fmt.Errorf("reading directory bytes: %w", err)
		}

		directory := pmtiles.DeserializeEntries(bytes.NewBuffer(b), header.InternalCompression)
		entry, ok := pmtiles.FindTile(directory, tileID)
		if ok {
			if entry.RunLength > 0 {
				// Found the tile
				tileReader, err := bucket.NewRangeReader(
					ctx,
					filename,
					int64(header.TileDataOffset+entry.Offset),
					int64(entry.Length),
				)
				if err != nil {
					return nil, fmt.Errorf("reading tile: %w", err)
				}
				tileBytes, err := io.ReadAll(tileReader)
				tileReader.Close()
				if err != nil {
					return nil, fmt.Errorf("reading tile bytes: %w", err)
				}
				return tileBytes, nil
			}
			// Leaf directory - continue search
			dirOffset = header.LeafDirectoryOffset + entry.Offset
			dirLength = uint64(entry.Length)
		} else {
			// Tile not found
			return nil, nil
		}
	}

	return nil, errors.New("exceeded max directory depth")
}

func formatGeometrySummary(geom orb.Geometry) string {
	if geom == nil {
		return "<nil>"
	}

	switch g := geom.(type) {
	case orb.Point:
		return fmt.Sprintf("Point(%.4f, %.4f)", g[0], g[1])
	case orb.MultiPoint:
		return fmt.Sprintf("MultiPoint(%d points)", len(g))
	case orb.LineString:
		return fmt.Sprintf("LineString(%d points)", len(g))
	case orb.MultiLineString:
		return fmt.Sprintf("MultiLineString(%d lines)", len(g))
	case orb.Polygon:
		totalPoints := 0
		for _, ring := range g {
			totalPoints += len(ring)
		}
		return fmt.Sprintf("Polygon(%d rings, %d points)", len(g), totalPoints)
	case orb.MultiPolygon:
		return fmt.Sprintf("MultiPolygon(%d polygons)", len(g))
	case orb.Collection:
		return fmt.Sprintf("Collection(%d geometries)", len(g))
	default:
		return fmt.Sprintf("%T", g)
	}
}

func lonLatToTile(lon, lat float64, zoom int) (int, int) {
	n := math.Pow(2, float64(zoom))
	x := int((lon + 180.0) / 360.0 * n)
	y := int((1.0 - math.Log(math.Tan(lat*math.Pi/180.0)+1.0/math.Cos(lat*math.Pi/180.0))/math.Pi) / 2.0 * n)
	return x, y
}

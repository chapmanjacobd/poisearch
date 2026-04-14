package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
	"github.com/protomaps/go-pmtiles/pmtiles"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
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
		return nil
	}

	ctx := context.Background()
	dir := filepath.Dir(*pmtilesPath)
	filename := filepath.Base(*pmtilesPath)
	bucket := pmtiles.NewFileBucket(dir)
	defer bucket.Close()

	// Read header
	header, err := readHeader(ctx, bucket, filename)
	if err != nil {
		return fmt.Errorf("reading header: %w", err)
	}

	fmt.Printf("File: %s\n", filename)
	fmt.Printf("MinZoom: %d, MaxZoom: %d\n", header.MinZoom, header.MaxZoom)
	fmt.Printf(
		"Center: (%f, %f) @ zoom %d\n",
		float64(header.CenterLonE7)/1e7,
		float64(header.CenterLatE7)/1e7,
		header.CenterZoom,
	)

	// Determine zoom range to query
	startZoom, endZoom, err := getZoomRange(header, *zoom)
	if err != nil {
		return err
	}

	fmt.Printf("\nQuerying point: lat=%f, lon=%f\n", *lat, *lon)

	qOpts := &queryOptions{
		bucket:      bucket,
		filename:    filename,
		header:      header,
		lon:         *lon,
		lat:         *lat,
		layerFilter: *layerName,
		maxFeatures: *maxFeatures,
		listLayers:  *listLayers,
	}

	for z := startZoom; z <= endZoom; z++ {
		qOpts.z = z
		if err := queryTile(ctx, qOpts); err != nil {
			if errors.Is(err, errDone) {
				return nil
			}
			fmt.Printf("  Query error at zoom %d: %v\n", z, err)
		}
	}

	return nil
}

func readHeader(ctx context.Context, bucket *pmtiles.FileBucket, filename string) (pmtiles.HeaderV3, error) {
	r, err := bucket.NewRangeReader(ctx, filename, 0, pmtiles.HeaderV3LenBytes)
	if err != nil {
		return pmtiles.HeaderV3{}, err
	}
	defer r.Close()

	headerBytes, err := io.ReadAll(r)
	if err != nil {
		return pmtiles.HeaderV3{}, err
	}

	return pmtiles.DeserializeHeader(headerBytes)
}

func getZoomRange(header pmtiles.HeaderV3, zoom int) (startZoom, endZoom int, err error) {
	startZoom = int(header.MinZoom)
	endZoom = int(header.MaxZoom)
	if zoom >= 0 {
		if uint8(zoom) < header.MinZoom || uint8(zoom) > header.MaxZoom {
			return 0, 0, fmt.Errorf("requested zoom %d is outside range [%d, %d]", zoom, header.MinZoom, header.MaxZoom)
		}
		startZoom = zoom
		endZoom = zoom
	}
	return startZoom, endZoom, nil
}

var errDone = errors.New("done")

type queryOptions struct {
	bucket      *pmtiles.FileBucket
	filename    string
	header      pmtiles.HeaderV3
	z           int
	lon, lat    float64
	layerFilter string
	maxFeatures int
	listLayers  bool
}

func queryTile(ctx context.Context, opts *queryOptions) error {
	x, y := lonLatToTile(opts.lon, opts.lat, opts.z)
	fmt.Printf("\n=== Zoom Level %d (Tile %d/%d) ===\n", opts.z, x, y)

	tOpts := &tileOptions{
		bucket:   opts.bucket,
		filename: opts.filename,
		header:   opts.header,
		z:        uint8(opts.z),
		x:        uint32(x),
		y:        uint32(y),
	}

	tileData, err := readTileDirect(ctx, tOpts)
	if err != nil {
		return fmt.Errorf("tile read error: %w", err)
	}
	if tileData == nil {
		fmt.Printf("  Tile not available\n")
		return nil
	}

	layers, err := mvt.Unmarshal(tileData)
	if err != nil {
		// Try gzipped
		layers, err = mvt.UnmarshalGzipped(tileData)
		if err != nil {
			return fmt.Errorf("failed to unmarshal MVT: %w", err)
		}
	}

	tile := maptile.New(uint32(x), uint32(y), maptile.Zoom(opts.z))

	// Collect layer names for listing mode
	var layerNames []string

	for _, layer := range layers {
		layerNames = append(layerNames, layer.Name)

		// Skip if filtering by layer name
		if opts.layerFilter != "" && layer.Name != opts.layerFilter {
			continue
		}

		layer.ProjectToWGS84(tile)

		fmt.Printf("\n  Layer: %s (%d features)\n", layer.Name, len(layer.Features))

		// Collect and display property keys from first few features
		propertyKeys := make(map[string]bool)
		sampleSize := min(opts.maxFeatures, len(layer.Features))

		for _, f := range layer.Features[:sampleSize] {
			for k := range f.Properties {
				propertyKeys[k] = true
			}
		}

		// Sort and display property keys
		keys := make([]string, 0, len(propertyKeys))
		for k := range propertyKeys {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Printf("    Properties: %s\n", strings.Join(keys, ", "))

		// Display features
		for i, f := range layer.Features {
			if i >= opts.maxFeatures {
				fmt.Printf("    ... and %d more features\n", len(layer.Features)-opts.maxFeatures)
				break
			}

			displayFeature(i, f)
		}
	}

	// List layers mode - exit after first successful tile
	if opts.listLayers {
		fmt.Printf("\nAvailable layers: %s\n", strings.Join(layerNames, ", "))
		return errDone
	}

	return nil
}

func displayFeature(idx int, f *geojson.Feature) {
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

	propsJSON, err := json.Marshal(props)
	if err != nil {
		propsJSON = fmt.Appendf(nil, "<error marshaling: %v>", err)
	}

	fmt.Printf("    [%d] ID: %v\n", idx, f.ID)
	fmt.Printf("        Geometry: %s\n", geomSummary)
	fmt.Printf("        Properties: %s\n", string(propsJSON))
}

type tileOptions struct {
	bucket   *pmtiles.FileBucket
	filename string
	header   pmtiles.HeaderV3
	z        uint8
	x, y     uint32
}

// readTileDirect reads a tile directly from the PMTiles file without using a server.
func readTileDirect(ctx context.Context, opts *tileOptions) ([]byte, error) {
	tileID := pmtiles.ZxyToID(opts.z, opts.x, opts.y)
	dirOffset := opts.header.RootOffset
	dirLength := opts.header.RootLength

	for range 4 {
		b, err := readDirectory(ctx, opts.bucket, opts.filename, dirOffset, dirLength)
		if err != nil {
			return nil, err
		}

		directory := pmtiles.DeserializeEntries(bytes.NewBuffer(b), opts.header.InternalCompression)
		entry, ok := pmtiles.FindTile(directory, tileID)
		if !ok {
			// Tile not found
			return nil, nil
		}

		if entry.RunLength > 0 {
			// Found the tile
			return readTileData(ctx, opts.bucket, opts.filename, opts.header, entry)
		}
		// Leaf directory - continue search
		dirOffset = opts.header.LeafDirectoryOffset + entry.Offset
		dirLength = uint64(entry.Length)
	}

	return nil, errors.New("exceeded max directory depth")
}

func readDirectory(
	ctx context.Context,
	bucket *pmtiles.FileBucket,
	filename string,
	offset, length uint64,
) ([]byte, error) {
	r, err := bucket.NewRangeReader(ctx, filename, int64(offset), int64(length))
	if err != nil {
		return nil, fmt.Errorf("reading directory: %w", err)
	}
	defer r.Close()

	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading directory bytes: %w", err)
	}
	return b, nil
}

func readTileData(
	ctx context.Context,
	bucket *pmtiles.FileBucket,
	filename string,
	header pmtiles.HeaderV3,
	entry pmtiles.EntryV3,
) ([]byte, error) {
	tileReader, err := bucket.NewRangeReader(
		ctx,
		filename,
		int64(header.TileDataOffset+entry.Offset),
		int64(entry.Length),
	)
	if err != nil {
		return nil, fmt.Errorf("reading tile: %w", err)
	}
	defer tileReader.Close()

	tileBytes, err := io.ReadAll(tileReader)
	if err != nil {
		return nil, fmt.Errorf("reading tile bytes: %w", err)
	}
	return tileBytes, nil
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

func lonLatToTile(lon, lat float64, zoom int) (x, y int) {
	n := math.Pow(2, float64(zoom))
	x = int((lon + 180.0) / 360.0 * n)
	y = int((1.0 - math.Log(math.Tan(lat*math.Pi/180.0)+1.0/math.Cos(lat*math.Pi/180.0))/math.Pi) / 2.0 * n)
	return x, y
}

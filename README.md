# poisearch

A lightweight (low RAM, medium disk) POI search engine for OpenStreetMap data.

## Features

- **Low RAM Build**: Processes PBF files in a single pass using streaming.
- **Customizable Importance**: Configure POI weights via TOML.
- **Multilingual Support**: Opt-in to index names in multiple languages.
- **Spatial Search**: Support for Geopoint (centroid) and Geoshape (bbox, simplified, full) indexing.
- **Fast Search**: Powered by Bleve v2.

## Prerequisites

- Go 1.22+
- libgeos (GEOS)
- `osmium` (for pre-processing PBFs)

### Installing GEOS

```sh
# macOS
brew install geos

# Fedora
sudo dnf install geos-devel

# Ubuntu / Debian
sudo apt install libgeos-dev
```

## Setup

1. **Pre-process OSM PBF**:
   `poisearch` requires PBF files to have node locations added to ways. You can do this with `osmium`:
   ```sh
   osmium add-locations-to-ways input.osm.pbf -o processed.osm.pbf
   ```

   **Handling Relations**:
   Large POIs like parks, universities, or city boundaries are often mapped as Relations. To index them efficiently:
   - `poisearch` supports Relations if they have a bounding box (`Bounds`) included in the PBF (e.g., from `osmium add-locations-to-ways`).
   - For complex multipolygon geometries, you can "flatten" relations into areas before indexing:
     ```sh
     osmium export processed.osm.pbf --geometry-types=polygon,point,linestring -o flat_pois.geojsonl
     ```
     *(Note: `poisearch` currently consumes PBF. For maximum reliability with complex relations, use osmium to filter/simplify into a PBF with locations added.)*

   To reduce index size, you can filter for specific tags first:
   ```sh
   osmium tags-filter processed.osm.pbf n/place n/amenity n/highway -o filtered.osm.pbf
   ```

2. **Configuration**:
   Copy `config.example.toml` to `config.toml` and adjust as needed.

## Usage

### Build the Index

```sh
go run ./cmd/poisearch --config config.toml build processed.osm.pbf
```

### Serve the API

```sh
go run ./cmd/poisearch --config config.toml serve
```

### Search

```sh
curl "http://localhost:8080/search?q=Berlin&limit=5"
```

Spatial search example:
```sh
curl "http://localhost:8080/search?q=Restaurant&lat=52.52&lon=13.40&radius=1000m"
```

## Performance & Geometry Modes

Based on benchmarks (Liechtenstein extract, 500m proximity):

| Data Type | Query Type | Avg Latency | Build Speed | Memory/Disk |
| :--- | :--- | :--- | :--- | :--- |
| **`geopoint`** | **Radius** | **~2.6ms** | **Fastest** | **Lowest** |
| `geopoint` | BBox | ~2.6ms | Fastest | Lowest |
| `geoshape` | Radius | ~14.7ms | Moderate | Medium |
| `geoshape` | BBox | ~11.6ms | Moderate | Medium |

**Recommendation**: Use **`geopoint`** with **`Radius`** for the best balance of performance, disk efficiency, and expected search behavior.

## Configuration Options

- `index_path`: Path to the Bleve index directory.
- `languages`: List of language codes to index (e.g., `["en", "zh"]`).
- `geometry_mode`:
  - `geopoint`: Index only the centroid/representative point.
  - `geoshape-bbox`: Index the bounding box.
  - `geoshape-simplified`: Index a simplified version of the geometry.
  - `geoshape-full`: Index the full geometry.
- `importance`: Weights for different POI types and boosts for population, capitals, and Wikipedia tags.

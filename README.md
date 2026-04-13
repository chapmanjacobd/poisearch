# poisearch

A lightweight (low RAM, medium disk) POI search engine for OpenStreetMap data.

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

   To reduce index size, you can filter for specific tags first:
   ```sh
   osmium tags-filter processed.osm.pbf n/* -o only.nodes.osm.pbf
   # or
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

## Benchmark Results

```plain
============================================================
INDEX SIZE COMPARISON
============================================================
Scenario             Index Size      Build Time     
------------------------------------------------------------
Leanest Mode         2.72 MB         967.992732ms   
No Geo               7.58 MB         2.27455587s    
Nodes Only           21.68 MB        2.961705339s   
Centroids (Simple)   29.82 MB        4.700223504s   
Representative Pts   30.82 MB        4.685882295s   
Simplified Shapes    37.17 MB        7.113649986s   
Raw Shapes           37.46 MB        7.147565174s   

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Centroids (Simple)   Subtype Filter            98.405µs        1         
Nodes Only           Class Filter              100.658µs       1         
Nodes Only           Subtype Filter            109.524µs       1         
Centroids (Simple)   Class Filter              112.86µs        1         
No Geo               Class Filter              126.709µs       1         
Representative Pts   Class Filter              132.149µs       1         
Representative Pts   Subtype Filter            151.221µs       1         
No Geo               Subtype Filter            151.932µs       1         
Simplified Shapes    Subtype Filter            154.531µs       1         
Raw Shapes           Class Filter              157.236µs       1         
Simplified Shapes    Class Filter              169.087µs       1         
Raw Shapes           Subtype Filter            236.526µs       1         
Nodes Only           Combined (Fuzzy+Class)    360.377µs       1         
No Geo               Combined (Fuzzy+Class)    483.69µs        1         
Simplified Shapes    Combined (Fuzzy+Class)    539.145µs       1         
Centroids (Simple)   Combined (Fuzzy+Class)    584.652µs       1         
Raw Shapes           Combined (Fuzzy+Class)    617.393µs       1         
Representative Pts   Combined (Fuzzy+Class)    805.674µs       1         
Nodes Only           Basic Search              834.731µs       76        
Nodes Only           Shop Search               873.249µs       10        
Nodes Only           Prefix Search             944.69µs        76        
Nodes Only           Fuzzy Search              1.048505ms      76        
Simplified Shapes    Shop Search               1.059146ms      10        
Raw Shapes           Shop Search               1.063863ms      10        
Centroids (Simple)   Shop Search               1.084177ms      10        
Leanest Mode         Basic Search              1.139756ms      76        
No Geo               Shop Search               1.140637ms      10        
No Geo               Basic Search              1.147961ms      91        
Centroids (Simple)   Basic Search              1.17369ms       88        
Nodes Only           Tourism Search            1.203725ms      7         
Representative Pts   Shop Search               1.254076ms      10        
Raw Shapes           Basic Search              1.309308ms      88        
Leanest Mode         Prefix Search             1.348378ms      76        
Representative Pts   Basic Search              1.365997ms      88        
Simplified Shapes    Basic Search              1.379412ms      88        
Centroids (Simple)   Prefix Search             1.381193ms      89        
Leanest Mode         Fuzzy Search              1.425305ms      76        
No Geo               Tourism Search            1.452823ms      12        
Centroids (Simple)   Tourism Search            1.524255ms      12        
Centroids (Simple)   Fuzzy Search              1.641106ms      88        
Raw Shapes           Prefix Search             1.650349ms      89        
Representative Pts   Tourism Search            1.680186ms      12        
Simplified Shapes    Prefix Search             1.704133ms      89        
Raw Shapes           Tourism Search            1.704186ms      12        
Representative Pts   Prefix Search             1.743407ms      89        
Representative Pts   Fuzzy Search              1.794914ms      88        
No Geo               Prefix Search             1.809488ms      92        
Raw Shapes           Fuzzy Search              1.85091ms       88        
No Geo               Fuzzy Search              1.881958ms      91        
Simplified Shapes    Tourism Search            1.884176ms      12        
Simplified Shapes    Fuzzy Search              1.899396ms      88        
Nodes Only           Radius Search             5.601765ms      795       
Representative Pts   BBox Search               5.903197ms      847       
Nodes Only           BBox Search               5.913199ms      847       
Representative Pts   Radius Search             5.919744ms      795       
Centroids (Simple)   Radius Search             6.205122ms      795       
Centroids (Simple)   BBox Search               6.543426ms      847       
Raw Shapes           BBox Search               53.501103ms     847       
Raw Shapes           Radius Search             54.262537ms     793       
Simplified Shapes    BBox Search               54.332013ms     847       
Simplified Shapes    Radius Search             54.477034ms     793
```

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
Leanest Mode         719.16 KB       304.414922ms   
No Geo               3.52 MB         1.10968034s    
Nodes Only           5.65 MB         725.259509ms   
Centroids (Simple)   11.63 MB        2.016247919s   
Representative Pts   11.63 MB        1.974063586s   
Simplified Shapes    12.89 MB        3.139264656s   
Raw Shapes           13.03 MB        3.576690529s   

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Nodes Only           Class Filter              108.638µs       1         
Centroids (Simple)   Subtype Filter            112.382µs       1         
Simplified Shapes    Subtype Filter            114.938µs       1         
Nodes Only           Subtype Filter            120.445µs       1         
No Geo               Subtype Filter            128.533µs       1         
Raw Shapes           Class Filter              146.934µs       1         
Raw Shapes           Subtype Filter            159.381µs       1         
Representative Pts   Class Filter              167.64µs        1         
No Geo               Class Filter              169.161µs       1         
Centroids (Simple)   Class Filter              175.572µs       1         
Simplified Shapes    Class Filter              176.444µs       1         
Representative Pts   Subtype Filter            195.941µs       1         
Nodes Only           Combined (Fuzzy+Class)    265.589µs       1         
No Geo               Combined (Fuzzy+Class)    515.498µs       1         
Simplified Shapes    Combined (Fuzzy+Class)    534.565µs       1         
Raw Shapes           Combined (Fuzzy+Class)    535.418µs       1         
Centroids (Simple)   Combined (Fuzzy+Class)    560.522µs       1         
Leanest Mode         Prefix Search             607.244µs       64        
Nodes Only           Prefix Search             668.456µs       64        
Representative Pts   Basic Search              673.097µs       71        
Leanest Mode         Fuzzy Search              679.075µs       64        
Leanest Mode         Basic Search              681.715µs       64        
Nodes Only           Basic Search              776.64µs        64        
No Geo               Basic Search              854.881µs       72        
Centroids (Simple)   Basic Search              890.885µs       71        
Nodes Only           Fuzzy Search              987.314µs       64        
Simplified Shapes    Basic Search              1.04421ms       71        
Simplified Shapes    Prefix Search             1.117473ms      71        
Representative Pts   Combined (Fuzzy+Class)    1.143846ms      1         
Raw Shapes           Basic Search              1.145567ms      71        
Representative Pts   Prefix Search             1.19068ms       71        
Centroids (Simple)   Prefix Search             1.21237ms       71        
Centroids (Simple)   Fuzzy Search              1.218716ms      71        
Raw Shapes           Prefix Search             1.230941ms      71        
No Geo               Prefix Search             1.236397ms      72        
Simplified Shapes    Fuzzy Search              1.329769ms      71        
No Geo               Fuzzy Search              1.438238ms      72        
Raw Shapes           Fuzzy Search              1.62641ms       71        
Nodes Only           BBox Search               2.098771ms      405       
Representative Pts   Fuzzy Search              2.102428ms      71        
Nodes Only           Radius Search             2.119056ms      383       
Representative Pts   BBox Search               2.435616ms      405       
Centroids (Simple)   Radius Search             2.492112ms      383       
Centroids (Simple)   BBox Search               2.706475ms      405       
Representative Pts   Radius Search             3.313858ms      383       
Simplified Shapes    Radius Search             12.815282ms     382       
Simplified Shapes    BBox Search               13.01392ms      405       
Raw Shapes           Radius Search             13.090436ms     382       
Raw Shapes           BBox Search               13.217321ms     405
```

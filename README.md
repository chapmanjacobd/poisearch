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
Leanest Mode         377.26 KB       154.059178ms   
No Geo               7.58 MB         2.171705902s   
Centroids (Simple)   30.82 MB        4.716917779s   
Representative Pts   30.83 MB        4.776278051s   
Nodes Only           32.70 MB        3.042061774s   
Simplified Shapes    36.35 MB        7.424434536s   
Raw Shapes           36.60 MB        7.392441424s   

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Nodes Only           Subtype Filter            73.84µs         1         
Nodes Only           Class Filter              91.153µs        1         
Representative Pts   Subtype Filter            123.246µs       1         
Raw Shapes           Subtype Filter            129.19µs        1         
Centroids (Simple)   Subtype Filter            141.272µs       1         
No Geo               Subtype Filter            148.055µs       1         
Representative Pts   Class Filter              155.96µs        1         
No Geo               Class Filter              163.935µs       1         
Simplified Shapes    Class Filter              168.71µs        1         
Simplified Shapes    Subtype Filter            176.421µs       1         
Raw Shapes           Class Filter              185.94µs        1         
Centroids (Simple)   Class Filter              257.009µs       1         
Nodes Only           Combined (Fuzzy+Class)    449.856µs       1         
Leanest Mode         Prefix Search             465.55µs        76        
No Geo               Combined (Fuzzy+Class)    508.289µs       1         
Leanest Mode         Basic Search              512.163µs       76        
Leanest Mode         Fuzzy Search              559.672µs       76        
Representative Pts   Combined (Fuzzy+Class)    578.296µs       1         
Raw Shapes           Combined (Fuzzy+Class)    655.342µs       1         
Simplified Shapes    Combined (Fuzzy+Class)    689.685µs       1         
Centroids (Simple)   Combined (Fuzzy+Class)    706.529µs       1         
Nodes Only           Tourism Search            830.622µs       7         
Nodes Only           Shop Search               875.638µs       10        
Nodes Only           Basic Search              894.633µs       76        
Centroids (Simple)   Basic Search              965.481µs       88        
Nodes Only           Prefix Search             1.073803ms      76        
No Geo               Shop Search               1.128289ms      10        
Raw Shapes           Shop Search               1.186509ms      10        
No Geo               Basic Search              1.19065ms       91        
Centroids (Simple)   Shop Search               1.20067ms       10        
Simplified Shapes    Shop Search               1.22555ms       10        
Representative Pts   Shop Search               1.25384ms       10        
Representative Pts   Basic Search              1.35971ms       88        
Simplified Shapes    Basic Search              1.404964ms      88        
Nodes Only           Fuzzy Search              1.462303ms      76        
Raw Shapes           Basic Search              1.469732ms      88        
No Geo               Prefix Search             1.523097ms      92        
Representative Pts   Tourism Search            1.574047ms      12        
Raw Shapes           Prefix Search             1.652839ms      89        
Centroids (Simple)   Fuzzy Search              1.666724ms      88        
No Geo               Tourism Search            1.685177ms      12        
No Geo               Fuzzy Search              1.685428ms      91        
Representative Pts   Prefix Search             1.730464ms      89        
Centroids (Simple)   Prefix Search             1.790053ms      89        
Simplified Shapes    Prefix Search             1.807518ms      89        
Simplified Shapes    Tourism Search            1.809715ms      12        
Raw Shapes           Fuzzy Search              1.843768ms      88        
Representative Pts   Fuzzy Search              1.864056ms      88        
Raw Shapes           Tourism Search            1.907806ms      12        
Simplified Shapes    Fuzzy Search              1.933357ms      88        
Centroids (Simple)   Tourism Search            2.14508ms       12        
Nodes Only           Radius Search             5.497521ms      795       
Centroids (Simple)   BBox Search               5.943482ms      847       
Representative Pts   Radius Search             6.090055ms      795       
Nodes Only           BBox Search               6.169602ms      847       
Representative Pts   BBox Search               6.25911ms       847       
Centroids (Simple)   Radius Search             7.055374ms      795       
Raw Shapes           BBox Search               55.725388ms     847       
Raw Shapes           Radius Search             56.074218ms     793       
Simplified Shapes    Radius Search             56.282908ms     793       
Simplified Shapes    BBox Search               56.401361ms     847
```

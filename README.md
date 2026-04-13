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
Raw PBF Scan         0 B (Live)      0s             
Leanest Mode         377.38 KB       162.940709ms   
No Geo               7.58 MB         2.21108384s    
Centroids (Simple)   29.82 MB        4.974954726s   
Representative Pts   29.82 MB        4.844081821s   
Nodes Only           32.71 MB        3.263363207s   
Raw Shapes           36.60 MB        7.228808615s   
Simplified Shapes    37.17 MB        7.641045936s   

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Representative Pts   Class Filter              117.59µs        1         
Representative Pts   Subtype Filter            122.704µs       1         
Raw Shapes           Class Filter              124.933µs       1         
Raw Shapes           Subtype Filter            133.289µs       1         
Nodes Only           Subtype Filter            136.559µs       1         
Simplified Shapes    Subtype Filter            147.738µs       1         
Nodes Only           Class Filter              149.283µs       1         
Simplified Shapes    Class Filter              161.482µs       1         
No Geo               Subtype Filter            177.9µs         1         
No Geo               Class Filter              188.987µs       1         
Centroids (Simple)   Class Filter              192.055µs       1         
Centroids (Simple)   Subtype Filter            192.217µs       1         
Leanest Mode         Prefix Search             495.815µs       76        
Leanest Mode         Basic Search              510.139µs       76        
Leanest Mode         Fuzzy Search              610.728µs       76        
Centroids (Simple)   Combined (Fuzzy+Class)    617.527µs       1         
Raw Shapes           Combined (Fuzzy+Class)    630.043µs       1         
Representative Pts   Combined (Fuzzy+Class)    646.148µs       1         
No Geo               Combined (Fuzzy+Class)    647.589µs       1         
Nodes Only           Combined (Fuzzy+Class)    691.133µs       1         
Simplified Shapes    Combined (Fuzzy+Class)    696.094µs       1         
Nodes Only           Basic Search              1.04618ms       76        
No Geo               Shop Search               1.127642ms      10        
No Geo               Basic Search              1.215104ms      91        
Nodes Only           Shop Search               1.217184ms      10        
Simplified Shapes    Shop Search               1.222026ms      10        
Nodes Only           Tourism Search            1.227549ms      7         
Centroids (Simple)   Shop Search               1.24851ms       10        
Representative Pts   Shop Search               1.252919ms      10        
Raw Shapes           Shop Search               1.286373ms      10        
Nodes Only           Prefix Search             1.358807ms      76        
Centroids (Simple)   Basic Search              1.423598ms      88        
No Geo               Prefix Search             1.456133ms      92        
Raw Shapes           Basic Search              1.488838ms      88        
Representative Pts   Basic Search              1.509319ms      88        
Nodes Only           Fuzzy Search              1.578852ms      76        
Simplified Shapes    Basic Search              1.58589ms       88        
Centroids (Simple)   Prefix Search             1.612075ms      89        
No Geo               Tourism Search            1.679696ms      12        
Representative Pts   Tourism Search            1.700125ms      12        
Centroids (Simple)   Tourism Search            1.712066ms      12        
Raw Shapes           Prefix Search             1.807794ms      89        
Centroids (Simple)   Fuzzy Search              1.815045ms      88        
Simplified Shapes    Prefix Search             1.824197ms      89        
Representative Pts   Prefix Search             1.889541ms      89        
Simplified Shapes    Tourism Search            1.897724ms      12        
No Geo               Fuzzy Search              1.915213ms      91        
Raw Shapes           Fuzzy Search              2.015557ms      88        
Raw Shapes           Tourism Search            2.039519ms      12        
Representative Pts   Fuzzy Search              2.094069ms      88        
Simplified Shapes    Fuzzy Search              2.224315ms      88        
Representative Pts   BBox Search               6.302289ms      847       
Nodes Only           Radius Search             6.605359ms      795       
Centroids (Simple)   BBox Search               6.635745ms      847       
Centroids (Simple)   Radius Search             6.684935ms      795       
Nodes Only           BBox Search               6.71807ms       847       
Representative Pts   Radius Search             6.802397ms      795       
Raw PBF Scan         Prefix Search             43.149492ms     50        
Raw PBF Scan         Basic Search              46.921996ms     50        
Raw PBF Scan         Fuzzy Search              49.077026ms     50        
Raw PBF Scan         Tourism Search            56.231057ms     12        
Raw Shapes           BBox Search               56.605315ms     847       
Simplified Shapes    BBox Search               57.677844ms     847       
Raw Shapes           Radius Search             57.891675ms     793       
Simplified Shapes    Radius Search             58.838378ms     793       
Raw PBF Scan         Subtype Filter            60.023059ms     1         
Raw PBF Scan         Shop Search               60.47798ms      10        
Raw PBF Scan         Class Filter              60.563448ms     1         
Raw PBF Scan         Combined (Fuzzy+Class)    63.093983ms     1
```

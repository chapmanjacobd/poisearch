# koisearch🎏

A lightweight (low RAM, medium disk) POI search engine for OpenStreetMap data

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
curl "http://localhost:9889/search?q=Berlin&limit=5"
```

Spatial search example:
```sh
curl "http://localhost:9889/search?q=Restaurant&lat=52.52&lon=13.40&radius=1000m"
```

## Benchmark Results

```plain
============================================================
INDEX SIZE COMPARISON
============================================================
Scenario             Index Size      Build Time     
------------------------------------------------------------
Raw PBF Scan         0 B (Live)      0s             
Leanest Mode         597.65 KB       287.44387ms    
No Geo               8.42 MB         3.601639024s   
Nodes Only           24.91 MB        3.674730788s   
Centroids (Simple)   51.85 MB        8.47519133s    
Representative Pts   51.86 MB        8.946382771s   
Simplified Shapes    98.51 MB        16.078916108s  
Raw Shapes           172.03 MB       28.295371387s  

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Simplified Shapes    Subtype Filter            123.362µs       1         
Representative Pts   Subtype Filter            128.686µs       1         
No Geo               Subtype Filter            132.133µs       1         
Nodes Only           Subtype Filter            134.599µs       1         
Representative Pts   Class Filter              143.934µs       1         
Simplified Shapes    Class Filter              154.851µs       1         
No Geo               Class Filter              159.341µs       1         
Centroids (Simple)   Class Filter              167.487µs       1         
Centroids (Simple)   Subtype Filter            168.004µs       1         
Nodes Only           Class Filter              169.668µs       1         
Raw Shapes           Class Filter              252.157µs       1         
Raw Shapes           Subtype Filter            296.575µs       1         
Nodes Only           Combined (Fuzzy+Class)    443.684µs       1         
Representative Pts   Combined (Fuzzy+Class)    466.319µs       1         
Leanest Mode         Prefix Search             508.938µs       76        
Centroids (Simple)   Combined (Fuzzy+Class)    559.761µs       1         
Leanest Mode         Basic Search              575.647µs       75        
No Geo               Combined (Fuzzy+Class)    607.555µs       1         
Simplified Shapes    Combined (Fuzzy+Class)    695.55µs        1         
Raw Shapes           Combined (Fuzzy+Class)    717.852µs       1         
Nodes Only           Tourism Search            837.819µs       7         
Leanest Mode         Fuzzy Search              844.459µs       75        
Nodes Only           Shop Search               911.126µs       10        
Raw Shapes           Shop Search               930.886µs       10        
No Geo               Shop Search               1.107923ms      10        
Representative Pts   Shop Search               1.12361ms       10        
Centroids (Simple)   Shop Search               1.155232ms      10        
Nodes Only           Basic Search              1.171612ms      75        
Nodes Only           Prefix Search             1.310296ms      76        
No Geo               Basic Search              1.337302ms      89        
Representative Pts   Basic Search              1.442349ms      89        
Centroids (Simple)   Basic Search              1.487313ms      89        
Nodes Only           Fuzzy Search              1.523809ms      75        
No Geo               Tourism Search            1.532217ms      12        
Simplified Shapes    Shop Search               1.538597ms      10        
Simplified Shapes    Basic Search              1.552882ms      89        
Centroids (Simple)   Prefix Search             1.554016ms      91        
Raw Shapes           Tourism Search            1.567485ms      12        
Representative Pts   Prefix Search             1.661566ms      91        
Representative Pts   Tourism Search            1.72343ms       12        
No Geo               Prefix Search             1.725116ms      91        
Centroids (Simple)   Tourism Search            1.737626ms      12        
Simplified Shapes    Prefix Search             1.782176ms      91        
Raw Shapes           Basic Search              1.792211ms      89        
No Geo               Fuzzy Search              1.819328ms      89        
Simplified Shapes    Tourism Search            1.860884ms      12        
Centroids (Simple)   Fuzzy Search              1.884145ms      89        
Representative Pts   Fuzzy Search              2.006007ms      89        
Raw Shapes           Fuzzy Search              2.060387ms      89        
Raw Shapes           Prefix Search             2.151269ms      91        
Simplified Shapes    Fuzzy Search              2.274356ms      89        
Nodes Only           BBox Search               5.826544ms      847       
Nodes Only           Radius Search             6.131946ms      795       
Representative Pts   Radius Search             13.179091ms     1568      
Representative Pts   BBox Search               13.263214ms     1699      
Centroids (Simple)   Radius Search             13.667876ms     1568      
Centroids (Simple)   BBox Search               14.836913ms     1700      
Raw Shapes           Radius Search             56.910012ms     795       
Raw Shapes           BBox Search               59.127678ms     849       
Simplified Shapes    Radius Search             62.489907ms     795       
Simplified Shapes    BBox Search               63.567435ms     849       
Raw PBF Scan         Fuzzy Search              93.932178ms     50        
Raw PBF Scan         Prefix Search             96.419006ms     50        
Raw PBF Scan         Basic Search              108.111482ms    50        
Raw PBF Scan         Class Filter              179.138219ms    1         
Raw PBF Scan         Combined (Fuzzy+Class)    186.359287ms    1         
Raw PBF Scan         Tourism Search            194.737782ms    12        
Raw PBF Scan         Shop Search               204.354257ms    10        
Raw PBF Scan         Subtype Filter            212.331429ms    1
```

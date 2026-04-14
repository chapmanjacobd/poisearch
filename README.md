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
Leanest Mode         389.28 KB       176.030685ms   
No Geo               9.21 MB         2.377612068s   
Nodes Only           22.28 MB        3.247340855s   
Representative Pts   31.20 MB        5.08027549s    
Centroids (Simple)   32.08 MB        5.033069612s   
Simplified Shapes    37.74 MB        7.625008888s   
Raw Shapes           37.98 MB        7.812550509s   

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Nodes Only           Subtype Filter            128.586µs       1         
Centroids (Simple)   Subtype Filter            133.665µs       1         
No Geo               Subtype Filter            138.501µs       1         
Nodes Only           Class Filter              147.662µs       1         
Raw Shapes           Subtype Filter            166.235µs       1         
Raw Shapes           Class Filter              167.613µs       1         
Centroids (Simple)   Class Filter              181.23µs        1         
No Geo               Class Filter              203.275µs       1         
Representative Pts   Class Filter              217.543µs       1         
Representative Pts   Subtype Filter            221.815µs       1         
Simplified Shapes    Subtype Filter            226.073µs       1         
Simplified Shapes    Class Filter              246.324µs       1         
Leanest Mode         Basic Search              472.302µs       76        
Leanest Mode         Prefix Search             556.318µs       76        
Nodes Only           Combined (Fuzzy+Class)    568.413µs       1         
Raw Shapes           Combined (Fuzzy+Class)    641.979µs       1         
No Geo               Combined (Fuzzy+Class)    642.443µs       1         
Centroids (Simple)   Combined (Fuzzy+Class)    685.442µs       1         
Leanest Mode         Fuzzy Search              709.959µs       76        
Representative Pts   Combined (Fuzzy+Class)    712.242µs       1         
Simplified Shapes    Combined (Fuzzy+Class)    744.756µs       1         
No Geo               Shop Search               1.01019ms       10        
Nodes Only           Tourism Search            1.017545ms      7         
Nodes Only           Basic Search              1.043831ms      76        
Nodes Only           Shop Search               1.180527ms      10        
Centroids (Simple)   Shop Search               1.20401ms       10        
Representative Pts   Shop Search               1.214165ms      10        
No Geo               Basic Search              1.252692ms      91        
Raw Shapes           Shop Search               1.305023ms      10        
Simplified Shapes    Shop Search               1.320351ms      10        
Nodes Only           Prefix Search             1.345184ms      76        
Centroids (Simple)   Basic Search              1.403402ms      88        
Raw Shapes           Basic Search              1.424458ms      88        
Representative Pts   Basic Search              1.517919ms      88        
Simplified Shapes    Basic Search              1.547966ms      88        
Centroids (Simple)   Prefix Search             1.650498ms      89        
Simplified Shapes    Tourism Search            1.703404ms      12        
Nodes Only           Fuzzy Search              1.709533ms      76        
Representative Pts   Prefix Search             1.710385ms      89        
No Geo               Tourism Search            1.742093ms      12        
No Geo               Prefix Search             1.746575ms      92        
Representative Pts   Tourism Search            1.774771ms      12        
Raw Shapes           Prefix Search             1.800831ms      89        
Simplified Shapes    Prefix Search             1.816461ms      89        
Raw Shapes           Tourism Search            1.849558ms      12        
Centroids (Simple)   Tourism Search            1.917478ms      12        
No Geo               Fuzzy Search              1.921593ms      91        
Centroids (Simple)   Fuzzy Search              1.983995ms      88        
Raw Shapes           Fuzzy Search              1.997696ms      88        
Simplified Shapes    Fuzzy Search              2.128642ms      88        
Representative Pts   Fuzzy Search              2.269648ms      88        
Representative Pts   BBox Search               6.383209ms      847       
Nodes Only           BBox Search               6.566799ms      847       
Nodes Only           Radius Search             6.588935ms      795       
Centroids (Simple)   Radius Search             6.734707ms      795       
Centroids (Simple)   BBox Search               6.736948ms      847       
Representative Pts   Radius Search             6.914951ms      795       
Raw PBF Scan         Basic Search              52.622274ms     50        
Raw PBF Scan         Fuzzy Search              53.026107ms     50        
Raw PBF Scan         Prefix Search             54.159268ms     50        
Raw Shapes           BBox Search               57.649651ms     847       
Raw Shapes           Radius Search             58.810683ms     793       
Simplified Shapes    BBox Search               60.27769ms      847       
Raw PBF Scan         Combined (Fuzzy+Class)    61.975474ms     1         
Simplified Shapes    Radius Search             62.060305ms     793       
Raw PBF Scan         Subtype Filter            67.665033ms     1         
Raw PBF Scan         Class Filter              69.965771ms     1         
Raw PBF Scan         Tourism Search            75.308159ms     12        
Raw PBF Scan         Shop Search               77.804552ms     10
```

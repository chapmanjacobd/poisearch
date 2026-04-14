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
Leanest Mode         606.21 KB       325.337969ms   
No Geo               3.19 MB         1.933625403s   
Nodes Only           25.57 MB        3.938718218s   
Centroids (Simple)   26.57 MB        4.40679527s    
Representative Pts   26.63 MB        4.46013963s    
Raw Shapes           30.33 MB        4.690481294s   
Simplified Shapes    30.34 MB        4.970328981s   

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
No Geo               Class Filter              110.551µs       1         
No Geo               Subtype Filter            118.045µs       1         
Nodes Only           Class Filter              140.05µs        1         
Representative Pts   Class Filter              147.698µs       1         
Centroids (Simple)   Subtype Filter            153.89µs        1         
Centroids (Simple)   Class Filter              155.618µs       1         
Representative Pts   Subtype Filter            170.163µs       1         
Raw Shapes           Subtype Filter            175.617µs       1         
Nodes Only           Subtype Filter            183.784µs       1         
Raw Shapes           Class Filter              186.296µs       1         
Simplified Shapes    Class Filter              222.097µs       1         
Simplified Shapes    Subtype Filter            251.198µs       1         
Representative Pts   Combined (Fuzzy+Class)    370.886µs       1         
No Geo               Combined (Fuzzy+Class)    426.226µs       1         
Raw Shapes           Combined (Fuzzy+Class)    502.125µs       1         
Nodes Only           Combined (Fuzzy+Class)    521.086µs       1         
Leanest Mode         Prefix Search             540.981µs       76        
Centroids (Simple)   Combined (Fuzzy+Class)    601.367µs       1         
Raw Shapes           Tourism Search            708.305µs       7         
Leanest Mode         Basic Search              726.449µs       75        
Centroids (Simple)   Shop Search               745.31µs        10        
No Geo               Tourism Search            749.491µs       7         
Simplified Shapes    Combined (Fuzzy+Class)    769.024µs       1         
No Geo               Shop Search               816.7µs         10        
Centroids (Simple)   Tourism Search            853.801µs       7         
Raw Shapes           Shop Search               864.055µs       10        
Nodes Only           Tourism Search            1.02065ms       7         
Nodes Only           Shop Search               1.057548ms      10        
No Geo               Basic Search              1.060528ms      76        
Representative Pts   Shop Search               1.065437ms      10        
Simplified Shapes    Shop Search               1.150321ms      10        
Simplified Shapes    Tourism Search            1.215241ms      7         
Representative Pts   Tourism Search            1.220899ms      7         
No Geo               Prefix Search             1.331193ms      77        
Leanest Mode         Fuzzy Search              1.343347ms      75        
Centroids (Simple)   Basic Search              1.507565ms      76        
No Geo               Fuzzy Search              1.601395ms      76        
Raw Shapes           Prefix Search             1.612359ms      77        
Simplified Shapes    Basic Search              1.626202ms      76        
Nodes Only           Basic Search              1.639882ms      75        
Centroids (Simple)   Prefix Search             1.677867ms      77        
Raw Shapes           Basic Search              1.769825ms      76        
Nodes Only           Prefix Search             1.872746ms      76        
Representative Pts   Basic Search              1.905875ms      76        
Representative Pts   Prefix Search             1.950275ms      77        
Simplified Shapes    Prefix Search             1.989507ms      77        
Raw Shapes           Fuzzy Search              2.027048ms      76        
Nodes Only           Fuzzy Search              2.04509ms       75        
Centroids (Simple)   Fuzzy Search              2.166184ms      76        
Simplified Shapes    Fuzzy Search              2.455042ms      76        
Representative Pts   Fuzzy Search              2.627249ms      76        
Nodes Only           Radius Search             6.354321ms      795       
Representative Pts   BBox Search               6.398878ms      848       
Nodes Only           BBox Search               7.121275ms      847       
Representative Pts   Radius Search             7.19704ms       796       
Centroids (Simple)   Radius Search             7.206596ms      796       
Centroids (Simple)   BBox Search               8.138033ms      848       
Raw Shapes           Radius Search             59.337798ms     794       
Raw Shapes           BBox Search               61.545639ms     848       
Simplified Shapes    Radius Search             62.349092ms     794       
Simplified Shapes    BBox Search               64.02293ms      848       
Raw PBF Scan         Combined (Fuzzy+Class)    190.744563ms    0         
Raw PBF Scan         Fuzzy Search              191.124698ms    0         
Raw PBF Scan         Tourism Search            195.65242ms     0         
Raw PBF Scan         Basic Search              198.542065ms    0         
Raw PBF Scan         Subtype Filter            203.121272ms    0         
Raw PBF Scan         Class Filter              203.479578ms    0         
Raw PBF Scan         Prefix Search             203.671552ms    0         
Raw PBF Scan         Shop Search               208.430465ms    0
```

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

## Benchmark Results (taiwan-latest.osm.pbf, 315.5 MB)

```plain
============================================================
INDEX SIZE COMPARISON
============================================================
Scenario             Index Size      Build Time     
------------------------------------------------------------
Raw PBF Scan         0 B (Live)      0s             
Leanest Mode         46.16 MB        49.567996263s  
No Geo               451.21 MB       3m57.919401728s
Nodes Only           658.99 MB       2m38.849800352s
Centroids (Simple)   2.84 GB         10m36.071305177s
Representative Pts   3.78 GB         10m34.130749293s
Simplified Shapes    6.90 GB         17m19.027871599s
Raw Shapes           11.64 GB        29m55.698060575s

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
No Geo               Subtype Filter            143.581µs       1         
No Geo               Class Filter              201.333µs       1         
Nodes Only           Subtype Filter            307.924µs       1         
Nodes Only           Class Filter              423.251µs       1         
Centroids (Simple)   Class Filter              753.774µs       1         
Centroids (Simple)   Subtype Filter            853.026µs       1         
Leanest Mode         Basic Search              1.562036ms      27        
No Geo               Basic Search              1.852116ms      34        
Raw Shapes           Subtype Filter            2.078714ms      1         
Simplified Shapes    Subtype Filter            2.396946ms      1         
No Geo               Combined (Fuzzy+Class)    2.437399ms      1         
Nodes Only           Basic Search              2.483766ms      29        
Representative Pts   Class Filter              2.487816ms      1         
Simplified Shapes    Class Filter              2.688007ms      1         
Leanest Mode         Fuzzy Search              2.778503ms      28        
Representative Pts   Subtype Filter            2.841368ms      1         
Nodes Only           Combined (Fuzzy+Class)    2.859872ms      1         
Raw Shapes           Class Filter              3.337324ms      1         
Leanest Mode         Prefix Search             3.648398ms      79        
No Geo               Fuzzy Search              3.936231ms      35        
Centroids (Simple)   Basic Search              4.087775ms      34        
Centroids (Simple)   Combined (Fuzzy+Class)    4.4986ms        1         
No Geo               Prefix Search             4.945575ms      100       
Nodes Only           Prefix Search             5.250877ms      82        
Nodes Only           Fuzzy Search              5.286691ms      30        
Simplified Shapes    Combined (Fuzzy+Class)    8.053996ms      1         
Raw Shapes           Combined (Fuzzy+Class)    8.438246ms      1         
Centroids (Simple)   Fuzzy Search              9.605673ms      35        
Representative Pts   Combined (Fuzzy+Class)    10.083315ms     1         
Centroids (Simple)   Prefix Search             11.204238ms     100       
Representative Pts   Basic Search              13.104646ms     34        
Simplified Shapes    Basic Search              13.854146ms     34        
Raw Shapes           Basic Search              14.756091ms     34        
Simplified Shapes    Fuzzy Search              18.180638ms     35        
Raw Shapes           Fuzzy Search              19.080597ms     35        
Nodes Only           Tourism Search            21.747755ms     217       
Representative Pts   Fuzzy Search              22.151011ms     35        
Simplified Shapes    Prefix Search             25.266027ms     100       
Raw Shapes           Prefix Search             25.361401ms     100       
Representative Pts   Prefix Search             30.285847ms     100       
Nodes Only           Shop Search               31.138671ms     1324      
Nodes Only           Radius Search             53.149121ms     844       
Nodes Only           BBox Search               55.72345ms      1587      
No Geo               Tourism Search            73.602324ms     686       
No Geo               Shop Search               74.750505ms     1372      
Centroids (Simple)   Shop Search               74.788932ms     1372      
Centroids (Simple)   Tourism Search            77.13065ms      686       
Simplified Shapes    Tourism Search            78.75498ms      686       
Raw Shapes           Tourism Search            79.475003ms     686       
Simplified Shapes    Shop Search               81.912879ms     1372      
Representative Pts   Shop Search               84.685353ms     1372      
Raw Shapes           Shop Search               84.942183ms     1372      
Representative Pts   Tourism Search            87.258609ms     686       
Centroids (Simple)   Radius Search             141.583718ms    2123      
Representative Pts   Radius Search             143.260187ms    2120      
Representative Pts   BBox Search               149.006299ms    3748      
Centroids (Simple)   BBox Search               151.724382ms    3751      
Raw PBF Scan         Prefix Search             386.528528ms    50        
Raw PBF Scan         Basic Search              917.914739ms    50        
Raw PBF Scan         Fuzzy Search              945.902616ms    50        
Raw PBF Scan         Shop Search               988.367008ms    50        
Simplified Shapes    Radius Search             1.188982764s    851       
Raw Shapes           Radius Search             1.192380895s    851       
Raw Shapes           BBox Search               1.249605957s    1594      
Simplified Shapes    BBox Search               1.294085988s    1594      
Raw PBF Scan         Tourism Search            3.124730713s    50        
Raw PBF Scan         Class Filter              40.904285701s   4         
Raw PBF Scan         Combined (Fuzzy+Class)    42.035228417s   4         
Raw PBF Scan         Subtype Filter            42.24231031s    2
```

## Benchmark Results (liechtenstein-latest.osm.pbf, 3.52 MB)
```plain
============================================================
INDEX SIZE COMPARISON
============================================================
Scenario             Index Size      Build Time     
------------------------------------------------------------
Raw PBF Scan         0 B (Live)      0s             
PMTiles Scan         0 B (Live)      0s             
Leanest Mode         994.23 KB       255.174221ms   
No Geo               12.11 MB        4.699672746s   
Nodes Only           28.71 MB        4.111308402s   
Centroids (Simple)   57.80 MB        10.776549086s  
Representative Pts   61.99 MB        10.815774486s  
Simplified Shapes    124.43 MB       19.976750824s  
Raw Shapes           204.90 MB       33.005565128s  

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
PMTiles Scan         Combined (Fuzzy+Class)    17.506µs        0         
PMTiles Scan         Tourism Search            17.53µs         0         
PMTiles Scan         Class Filter              17.779µs        0         
PMTiles Scan         Subtype Filter            17.893µs        0         
PMTiles Scan         Fuzzy Search              20.86µs         0         
PMTiles Scan         Prefix Search             21.972µs        0         
PMTiles Scan         Shop Search               22.031µs        0         
PMTiles Scan         Basic Search              25.79µs         0         
Simplified Shapes    Subtype Filter            117.741µs       1         
No Geo               Class Filter              138.71µs        1         
Representative Pts   Subtype Filter            160.934µs       1         
Simplified Shapes    Class Filter              161.63µs        1         
Centroids (Simple)   Subtype Filter            170.012µs       1         
Centroids (Simple)   Class Filter              170.872µs       1         
Nodes Only           Class Filter              176.27µs        1         
Raw Shapes           Class Filter              184.347µs       1         
Representative Pts   Class Filter              184.89µs        1         
Nodes Only           Subtype Filter            190.3µs         1         
No Geo               Subtype Filter            217.645µs       1         
Raw Shapes           Subtype Filter            221.276µs       1         
PMTiles Scan         BBox Search               431.023µs       0         
No Geo               Combined (Fuzzy+Class)    512.71µs        1         
Nodes Only           Combined (Fuzzy+Class)    558.095µs       1         
Simplified Shapes    Combined (Fuzzy+Class)    564.686µs       1         
Centroids (Simple)   Combined (Fuzzy+Class)    589.739µs       1         
Raw Shapes           Combined (Fuzzy+Class)    591.248µs       1         
PMTiles Scan         Radius Search             614.83µs        0         
Leanest Mode         Basic Search              642.503µs       126       
Leanest Mode         Prefix Search             697.63µs        127       
Representative Pts   Combined (Fuzzy+Class)    783.213µs       1         
Leanest Mode         Fuzzy Search              860.232µs       126       
No Geo               Shop Search               1.00523ms       10        
Nodes Only           Tourism Search            1.060974ms      7         
Simplified Shapes    Shop Search               1.071905ms      10        
Nodes Only           Shop Search               1.12208ms       10        
Raw Shapes           Shop Search               1.130399ms      10        
Representative Pts   Shop Search               1.143011ms      10        
Centroids (Simple)   Shop Search               1.164687ms      10        
Nodes Only           Basic Search              1.46848ms       126       
No Geo               Basic Search              1.724892ms      178       
Nodes Only           Prefix Search             1.763775ms      127       
Representative Pts   Tourism Search            1.796662ms      12        
Simplified Shapes    Tourism Search            1.805358ms      12        
No Geo               Tourism Search            1.807406ms      12        
Centroids (Simple)   Tourism Search            1.807908ms      12        
Nodes Only           Fuzzy Search              1.847556ms      126       
Centroids (Simple)   Basic Search              1.871123ms      178       
Raw Shapes           Tourism Search            1.935957ms      12        
Representative Pts   Basic Search              1.995797ms      178       
No Geo               Prefix Search             2.104715ms      181       
Raw Shapes           Basic Search              2.151906ms      178       
No Geo               Fuzzy Search              2.196582ms      178       
Simplified Shapes    Basic Search              2.204643ms      178       
Centroids (Simple)   Prefix Search             2.292313ms      181       
Centroids (Simple)   Fuzzy Search              2.346468ms      178       
Simplified Shapes    Prefix Search             2.373147ms      181       
Representative Pts   Fuzzy Search              2.481697ms      178       
Representative Pts   Prefix Search             2.537979ms      181       
Raw Shapes           Prefix Search             2.578791ms      181       
Simplified Shapes    Fuzzy Search              2.733371ms      178       
Raw Shapes           Fuzzy Search              2.752057ms      178       
Nodes Only           BBox Search               7.665104ms      969       
Nodes Only           Radius Search             7.669166ms      911       
Centroids (Simple)   BBox Search               14.883056ms     2067      
Representative Pts   BBox Search               15.135129ms     2066      
Centroids (Simple)   Radius Search             15.402315ms     1913      
Representative Pts   Radius Search             16.031052ms     1914      
Raw PBF Scan         Fuzzy Search              34.264248ms     50        
Raw PBF Scan         Prefix Search             40.084129ms     50        
Raw PBF Scan         Basic Search              43.216992ms     50        
Simplified Shapes    BBox Search               62.167194ms     983       
Raw Shapes           BBox Search               62.351335ms     983       
Raw Shapes           Radius Search             63.467078ms     923       
Simplified Shapes    Radius Search             63.787139ms     923       
Raw PBF Scan         Tourism Search            116.881437ms    12        
Raw PBF Scan         Subtype Filter            120.010416ms    1         
Raw PBF Scan         Combined (Fuzzy+Class)    120.203983ms    1         
Raw PBF Scan         Shop Search               122.921763ms    10        
Raw PBF Scan         Class Filter              123.284459ms    1
```
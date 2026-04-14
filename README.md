# poisearch 🥡

A lightweight (low RAM, medium disk) [POI](https://en.wikipedia.org/wiki/Poi_(food)) search engine for OpenStreetMap data

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
Leanest Mode         994.48 KB       309.04557ms    
No Geo               13.98 MB        4.619546702s   
Nodes Only           24.37 MB        4.185054384s   
Centroids (Simple)   59.94 MB        10.586035994s  
Representative Pts   59.96 MB        10.499683885s  
Simplified Shapes    126.35 MB       19.463445524s  
Raw Shapes           215.01 MB       33.471746435s  

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
PMTiles Scan         Shop Search               11.777µs        0         
PMTiles Scan         Combined (Fuzzy+Class)    12.864µs        0         
PMTiles Scan         Tourism Search            14.921µs        0         
PMTiles Scan         Subtype Filter            19.428µs        0         
PMTiles Scan         Class Filter              19.745µs        0         
PMTiles Scan         Fuzzy Search              20.625µs        0         
PMTiles Scan         Prefix Search             20.756µs        0         
PMTiles Scan         Basic Search              25.749µs        0         
Representative Pts   Subtype Filter            109.697µs       1         
Centroids (Simple)   Class Filter              117.506µs       1         
Simplified Shapes    Subtype Filter            119.112µs       1         
Centroids (Simple)   Subtype Filter            123.444µs       1         
Simplified Shapes    Class Filter              140.438µs       1         
Representative Pts   Class Filter              145.282µs       1         
Nodes Only           Class Filter              162.564µs       1         
No Geo               Class Filter              167.281µs       1         
No Geo               Subtype Filter            180.428µs       1         
Raw Shapes           Subtype Filter            182.337µs       1         
Nodes Only           Subtype Filter            205.056µs       1         
Raw Shapes           Class Filter              205.449µs       1         
Simplified Shapes    Combined (Fuzzy+Class)    495.948µs       1         
No Geo               Combined (Fuzzy+Class)    536.182µs       1         
Representative Pts   Combined (Fuzzy+Class)    538.574µs       1         
Centroids (Simple)   Combined (Fuzzy+Class)    542.103µs       1         
Nodes Only           Combined (Fuzzy+Class)    570.585µs       1         
Leanest Mode         Basic Search              572.04µs        126       
Raw Shapes           Combined (Fuzzy+Class)    582.298µs       1         
Leanest Mode         Prefix Search             596.642µs       127       
PMTiles Scan         BBox Search               602.793µs       0         
PMTiles Scan         Radius Search             612.856µs       0         
Leanest Mode         Fuzzy Search              763.039µs       126       
Representative Pts   Shop Search               906.046µs       10        
No Geo               Shop Search               909.455µs       10        
Nodes Only           Tourism Search            922.042µs       7         
Simplified Shapes    Shop Search               1.032522ms      10        
Nodes Only           Shop Search               1.044785ms      10        
Centroids (Simple)   Shop Search               1.129457ms      10        
Raw Shapes           Shop Search               1.191976ms      10        
Nodes Only           Basic Search              1.357175ms      126       
Nodes Only           Prefix Search             1.447512ms      127       
No Geo               Tourism Search            1.619363ms      12        
Representative Pts   Tourism Search            1.678816ms      12        
Centroids (Simple)   Tourism Search            1.725636ms      12        
Simplified Shapes    Tourism Search            1.740692ms      12        
No Geo               Prefix Search             1.742781ms      181       
No Geo               Basic Search              1.749598ms      178       
Raw Shapes           Tourism Search            1.792773ms      12        
Nodes Only           Fuzzy Search              1.807405ms      126       
Representative Pts   Basic Search              1.851102ms      178       
No Geo               Fuzzy Search              1.946742ms      178       
Centroids (Simple)   Basic Search              2.090189ms      178       
Simplified Shapes    Basic Search              2.109781ms      178       
Representative Pts   Prefix Search             2.191362ms      181       
Raw Shapes           Basic Search              2.273722ms      178       
Centroids (Simple)   Fuzzy Search              2.310216ms      178       
Centroids (Simple)   Prefix Search             2.319466ms      181       
Representative Pts   Fuzzy Search              2.353162ms      178       
Simplified Shapes    Prefix Search             2.632863ms      181       
Raw Shapes           Prefix Search             2.638416ms      181       
Simplified Shapes    Fuzzy Search              2.68402ms       178       
Raw Shapes           Fuzzy Search              2.921132ms      178       
Nodes Only           BBox Search               7.031421ms      969       
Nodes Only           Radius Search             8.513285ms      1617      
Representative Pts   BBox Search               14.545196ms     2066      
Centroids (Simple)   BBox Search               14.800167ms     2067      
Representative Pts   Radius Search             18.064792ms     3802      
Centroids (Simple)   Radius Search             18.129512ms     3803      
Raw PBF Scan         Prefix Search             34.308452ms     50        
Raw PBF Scan         Fuzzy Search              34.824464ms     50        
Raw PBF Scan         Basic Search              36.348618ms     50        
Raw Shapes           BBox Search               61.632722ms     983       
Simplified Shapes    BBox Search               62.835385ms     983       
Simplified Shapes    Radius Search             64.7204ms       1635      
Raw Shapes           Radius Search             67.12751ms      1635      
Raw PBF Scan         Class Filter              112.44683ms     1         
Raw PBF Scan         Tourism Search            114.491391ms    12        
Raw PBF Scan         Combined (Fuzzy+Class)    114.776879ms    1         
Raw PBF Scan         Shop Search               121.142644ms    10        
Raw PBF Scan         Subtype Filter            121.360389ms    1
```

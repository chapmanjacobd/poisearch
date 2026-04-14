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
Leanest Mode         994.08 KB       284.43ms       
No Geo               12.11 MB        4.69s          
Nodes Only           29.37 MB        4.22s          
Centroids (Simple)   57.79 MB        10.60s         
Cached Searches      58.79 MB        10.69s         
Addresses            59.65 MB        11.03s         
Representative Pts   59.95 MB        10.95s         
Wiki Redirects       61.99 MB        11.10s         
Bounding Boxes       98.84 MB        14.49s         

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Cached Searches      Class Filter              2.38µs          1         
Cached Searches      Combined (Fuzzy+Class)    4.96µs          1         
Cached Searches      Subtype Filter            7.12µs          1         
Cached Searches      Shop Search               7.48µs          10        
PMTiles Scan         Shop Search               9.93µs          0         
PMTiles Scan         Prefix Search             10.17µs         0         
PMTiles Scan         Tourism Search            11.02µs         0         
PMTiles Scan         Class Filter              12.41µs         0         
PMTiles Scan         Fuzzy Search              14.81µs         0         
PMTiles Scan         Combined (Fuzzy+Class)    16.95µs         0         
Cached Searches      Fuzzy Search              18.26µs         178       
Addresses            Address Match             21.12µs         0         
Cached Searches      Tourism Search            21.85µs         12        
PMTiles Scan         Subtype Filter            26.17µs         0         
Cached Searches      Prefix Search             28.16µs         181       
PMTiles Scan         Basic Search              29.02µs         0         
Cached Searches      Basic Search              34.66µs         178       
Cached Searches      BBox Search               76.27µs         2067      
Cached Searches      Radius Search             112.28µs        3803      
Representative Pts   Subtype Filter            123.70µs        1         
Centroids (Simple)   Subtype Filter            145.19µs        1         
Addresses            Subtype Filter            156.47µs        1         
No Geo               Subtype Filter            156.99µs        1         
Wiki Redirects       Class Filter              160.86µs        1         
No Geo               Class Filter              180.41µs        1         
Nodes Only           Class Filter              181.97µs        1         
Wiki Redirects       Subtype Filter            185.42µs        1         
Bounding Boxes       Subtype Filter            185.82µs        1         
Bounding Boxes       Class Filter              188.78µs        1         
Nodes Only           Subtype Filter            194.27µs        1         
Addresses            Class Filter              200.07µs        1         
Centroids (Simple)   Class Filter              204.74µs        1         
Representative Pts   Class Filter              217.67µs        1         
No Geo               Combined (Fuzzy+Class)    494.86µs        1         
Bounding Boxes       Combined (Fuzzy+Class)    569.33µs        1         
Centroids (Simple)   Combined (Fuzzy+Class)    572.03µs        1         
Leanest Mode         Basic Search              574.70µs        126       
Nodes Only           Combined (Fuzzy+Class)    587.98µs        1         
Leanest Mode         Prefix Search             608.35µs        127       
Representative Pts   Combined (Fuzzy+Class)    612.76µs        1         
Addresses            Combined (Fuzzy+Class)    650.21µs        1         
Wiki Redirects       Combined (Fuzzy+Class)    714.48µs        1         
Leanest Mode         Fuzzy Search              811.85µs        126       
Representative Pts   Shop Search               1.09ms          10        
Bounding Boxes       Shop Search               1.10ms          10        
Addresses            Shop Search               1.12ms          10        
Nodes Only           Tourism Search            1.12ms          7         
No Geo               Shop Search               1.14ms          10        
Nodes Only           Shop Search               1.15ms          10        
Wiki Redirects       Shop Search               1.23ms          10        
Centroids (Simple)   Shop Search               1.37ms          10        
Nodes Only           Basic Search              1.41ms          126       
Nodes Only           Prefix Search             1.64ms          127       
No Geo               Tourism Search            1.76ms          12        
No Geo               Basic Search              1.81ms          178       
Wiki Redirects       Basic Search              1.87ms          178       
Addresses            Tourism Search            1.88ms          12        
Wiki Redirects       Tourism Search            1.91ms          12        
Bounding Boxes       Tourism Search            1.92ms          12        
No Geo               Prefix Search             1.97ms          181       
Representative Pts   Tourism Search            1.99ms          12        
Centroids (Simple)   Tourism Search            2.00ms          12        
Centroids (Simple)   Basic Search              2.02ms          178       
Representative Pts   Basic Search              2.02ms          178       
Nodes Only           Fuzzy Search              2.04ms          126       
No Geo               Fuzzy Search              2.18ms          178       
Bounding Boxes       Basic Search              2.23ms          178       
Addresses            Basic Search              2.28ms          178       
Addresses            Prefix Search             2.38ms          181       
Centroids (Simple)   Prefix Search             2.39ms          181       
Representative Pts   Prefix Search             2.41ms          181       
Centroids (Simple)   Fuzzy Search              2.44ms          178       
Representative Pts   Fuzzy Search              2.45ms          178       
Wiki Redirects       Prefix Search             2.49ms          181       
Bounding Boxes       Prefix Search             2.51ms          181       
Addresses            Fuzzy Search              2.56ms          178       
Bounding Boxes       Fuzzy Search              2.65ms          178       
Wiki Redirects       Fuzzy Search              2.66ms          178       
PMTiles Scan         Radius Search             3.13ms          1         
PMTiles Scan         BBox Search               4.14ms          0         
Nodes Only           BBox Search               7.29ms          969       
Nodes Only           Radius Search             8.68ms          1617      
Representative Pts   BBox Search               15.23ms         2066      
Wiki Redirects       BBox Search               16.03ms         2067      
Addresses            BBox Search               16.37ms         2067      
Centroids (Simple)   BBox Search               17.12ms         2067      
Centroids (Simple)   Radius Search             19.75ms         3803      
Wiki Redirects       Radius Search             20.18ms         3803      
Addresses            Radius Search             20.70ms         3803      
Representative Pts   Radius Search             21.06ms         3802      
Raw PBF Scan         Basic Search              39.02ms         50        
Raw PBF Scan         Fuzzy Search              39.70ms         50        
Raw PBF Scan         Prefix Search             40.48ms         50        
Bounding Boxes       BBox Search               66.31ms         983       
Bounding Boxes       Radius Search             67.80ms         1635      
Raw PBF Scan         Combined (Fuzzy+Class)    113.05ms        1         
Raw PBF Scan         Subtype Filter            124.04ms        1         
Raw PBF Scan         Shop Search               124.73ms        10        
Raw PBF Scan         Class Filter              132.58ms        1         
Raw PBF Scan         Tourism Search            134.52ms        12
```

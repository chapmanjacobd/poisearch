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

1. Pre-process OSM PBF:
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

2. Configuration:
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

Metadata and direct PBF examples:
```sh
# Search for wheelchair accessible POIs
curl "http://localhost:9889/search?wheelchair=yes&city=Berlin"

# Direct PBF search (no index needed)
curl "http://localhost:9889/search?q=museum&mode=pbf"

# Search by phone number
curl "http://localhost:9889/search?phone=123456"
```

## API Documentation

The following query parameters are supported on `/search`:

| Parameter | Type | Example | Description |
| :--- | :--- | :--- | :--- |
| `q` | string | `Berlin` | Search query string |
| `mode` | string | `pbf` | "pbf" or "pmtiles" for live search against those file types (skips index) |
| `format` | string | `text` | "text" for flat key-value response (UNIX-pipe friendly) |
| `lat`, `lon` | float | `52.52`, `13.40` | Center coordinates for spatial search |
| `radius` | string | `1000m` | Radius (e.g. "1000m", "5km") for spatial search |
| `min_lat`, `max_lat`, `min_lon`, `max_lon` | float | `52.4`, `52.6`, `13.3`, `13.5` | Bounding box coordinates for spatial search |
| `limit` | int | `10` | Maximum number of results to return (default: 10) |
| `from` | int | `0` | Offset for pagination |
| `langs` | string | `en,de` | Comma-separated list of preferred languages |
| `fuzzy` | bool | `true` | Toggle fuzzy matching (true/1) |
| `prefix` | bool | `true` | Toggle prefix matching (true/1) |
| `class`, `subtype` | string | `amenity`, `restaurant` | Filter by primary classification |
| `classes`, `subtypes` | string | `amenity,shop` | Comma-separated multi-value filters |

### Supported Tags

| Category | Searchable Keys | Returned Fields |
| :--- | :--- | :--- |
| Names | `name`, `alt_name`, `short_name`, `old_name`, `brand`, `operator`, `name:lang` | Full name set + enhanced name |
| Classification | `amenity`, `shop`, `tourism`, `leisure`, `place`, `highway`, etc. | `class`, `subtype`, `classes`, `subtypes` |
| Address | `addr:street`, `addr:housenumber`, `addr:city`, `addr:postcode`, `addr:country`, `addr:floor`, `addr:unit`, `level` | Full address set |
| Metadata | `phone`, `opening_hours`, `wheelchair`, `wikidata`, `wikipedia` | `phone`, `opening_hours`, `wheelchair`, `importance` |

## Options

`config.toml` defines both index creation time and runtime options. There are three main search strategies, each with different tradeoffs:

| Feature/Tag | Bleve Index | PBF (`--mode=pbf`) | Raw PMTiles (`--mode=pmtiles`) |
| :--- | :--- | :--- | :--- |
| Searchable Fields | Explicitly mapped fields (Name, Address, Metadata, Class). Fast & Scalable. | Full Tag Set. Matches against all OSM tags via arbitrary data fallback. | Limited by PMTiles schema (default: OpenMapTiles). |
| Performance | High. Optimized for large datasets and complex queries. | Low. Linear scan of the PBF file. Best for small files. | Medium. Spatial indexing allows fast lookups without a full index. |
| Address Support | Strong. Indexed `addr:*` tags including floor, unit, and level. | Strong. Direct access to all address tags in the PBF. | Weak. Most address tags are stripped by default OMT schema. |
| Classification | Static. Pre-calculated at index time for maximum speed. | Dynamic. Re-calculates classification on every scan. | Heuristic. Re-maps generic OMT classes back to OSM tags. |
| Custom Tags | Requires updating `mapping.go` and re-indexing. | High. Instant support for any tag via `TagMap()`. | Very Low. Limited to what `planetiler` preserves. |

## Benchmark Results (liechtenstein-latest.osm.pbf, 3.52 MB)
```plain
============================================================
INDEX SIZE COMPARISON
============================================================
Scenario             Index Size      Build Time     
------------------------------------------------------------
PBF Scan             0 B (Live)      0s             
PMTiles Scan         0 B (Live)      0s             
Minimal Mode         1.30 MB         284.04ms       
No Geo               13.21 MB        4.66s          
Nodes Only           30.28 MB        3.44s          
Representative Pts   60.40 MB        11.01s         
Centroids (Simple)   60.42 MB        10.31s         
Addresses            62.42 MB        10.55s         
Cached Searches      62.70 MB        10.32s         
Wiki Redirects       63.89 MB        10.57s         
Bounding Boxes       92.24 MB        13.83s         

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Cached Searches      Subtype Filter            1.66µs          1         
Cached Searches      Class Filter              1.83µs          1         
Cached Searches      Combined (Fuzzy+Class)    3.77µs          1         
Cached Searches      Shop Search               5.49µs          10        
Cached Searches      Prefix Search             10.28µs         186       
Cached Searches      Fuzzy Search              12.63µs         183       
Cached Searches      Basic Search              12.77µs         183       
Cached Searches      Tourism Search            19.09µs         12        
Cached Searches      Radius Search             78.42µs         3803      
Cached Searches      BBox Search               98.62µs         2067      
No Geo               Subtype Filter            103.11µs        1         
Addresses            Subtype Filter            115.65µs        1         
Centroids (Simple)   Subtype Filter            126.38µs        1         
Nodes Only           Subtype Filter            149.69µs        1         
Wiki Redirects       Subtype Filter            154.96µs        1         
Nodes Only           Class Filter              165.68µs        1         
Centroids (Simple)   Class Filter              166.01µs        1         
Representative Pts   Subtype Filter            171.83µs        1         
No Geo               Class Filter              174.66µs        1         
Bounding Boxes       Subtype Filter            176.83µs        1         
Representative Pts   Class Filter              195.26µs        1         
Addresses            Class Filter              210.05µs        1         
Wiki Redirects       Class Filter              214.78µs        1         
Bounding Boxes       Class Filter              225.42µs        1         
Minimal Mode         Prefix Search             419.44µs        127       
No Geo               Combined (Fuzzy+Class)    424.68µs        1         
Minimal Mode         Basic Search              446.01µs        126       
Nodes Only           Combined (Fuzzy+Class)    504.76µs        1         
Addresses            Combined (Fuzzy+Class)    517.72µs        1         
Centroids (Simple)   Combined (Fuzzy+Class)    553.86µs        1         
Representative Pts   Combined (Fuzzy+Class)    574.79µs        1         
Wiki Redirects       Combined (Fuzzy+Class)    645.00µs        1         
Minimal Mode         Fuzzy Search              645.06µs        126       
Bounding Boxes       Combined (Fuzzy+Class)    735.44µs        1         
No Geo               Shop Search               919.96µs        10        
Centroids (Simple)   Shop Search               1.02ms          10        
Bounding Boxes       Shop Search               1.04ms          10        
PMTiles Scan         Radius Search             1.07ms          50        
Nodes Only           Shop Search               1.10ms          10        
Nodes Only           Tourism Search            1.15ms          7         
Addresses            Shop Search               1.15ms          10        
Nodes Only           Basic Search              1.17ms          126       
Wiki Redirects       Shop Search               1.18ms          10        
Representative Pts   Shop Search               1.25ms          10        
Nodes Only           Prefix Search             1.38ms          127       
No Geo               Tourism Search            1.41ms          12        
Addresses            Address Match             1.47ms          7         
No Geo               Basic Search              1.51ms          183       
Centroids (Simple)   Tourism Search            1.52ms          12        
Nodes Only           Fuzzy Search              1.57ms          126       
PMTiles Scan         BBox Search               1.59ms          50        
No Geo               Prefix Search             1.68ms          186       
Wiki Redirects       Tourism Search            1.76ms          12        
Addresses            Tourism Search            1.80ms          12        
Bounding Boxes       Tourism Search            1.85ms          12        
Representative Pts   Tourism Search            1.89ms          12        
No Geo               Fuzzy Search              1.89ms          183       
Centroids (Simple)   Prefix Search             2.10ms          186       
Wiki Redirects       Basic Search              2.11ms          183       
Addresses            Basic Search              2.18ms          183       
Centroids (Simple)   Basic Search              2.19ms          183       
Representative Pts   Basic Search              2.28ms          183       
Addresses            Prefix Search             2.47ms          186       
Centroids (Simple)   Fuzzy Search              2.48ms          183       
Bounding Boxes       Basic Search              2.52ms          183       
Addresses            Fuzzy Search              2.56ms          183       
Representative Pts   Prefix Search             2.58ms          186       
Bounding Boxes       Prefix Search             2.61ms          186       
Wiki Redirects       Prefix Search             2.66ms          186       
Representative Pts   Fuzzy Search              2.78ms          183       
Wiki Redirects       Fuzzy Search              2.79ms          183       
Bounding Boxes       Fuzzy Search              2.81ms          183       
PMTiles Scan         Basic Search              3.04ms          50        
PMTiles Scan         Fuzzy Search              3.52ms          50        
PMTiles Scan         Prefix Search             3.87ms          50        
PMTiles Scan         Tourism Search            6.52ms          3         
Nodes Only           BBox Search               6.78ms          969       
PMTiles Scan         Shop Search               7.26ms          5         
PMTiles Scan         Combined (Fuzzy+Class)    7.75ms          4         
PMTiles Scan         Class Filter              8.18ms          4         
Nodes Only           Radius Search             8.79ms          1617      
PMTiles Scan         Subtype Filter            8.89ms          4         
Centroids (Simple)   BBox Search               13.33ms         2067      
Representative Pts   BBox Search               15.16ms         2066      
Addresses            BBox Search               15.20ms         2067      
Wiki Redirects       BBox Search               15.27ms         2067      
Centroids (Simple)   Radius Search             17.98ms         3803      
Wiki Redirects       Radius Search             18.68ms         3803      
Addresses            Radius Search             19.13ms         3803      
Representative Pts   Radius Search             19.79ms         3802      
PBF Scan             Fuzzy Search              28.86ms         50        
PBF Scan             Prefix Search             29.15ms         50        
PBF Scan             Basic Search              33.31ms         50        
Bounding Boxes       BBox Search               62.04ms         983       
Bounding Boxes       Radius Search             64.91ms         1635      
PBF Scan             Subtype Filter            124.68ms        1         
PBF Scan             Combined (Fuzzy+Class)    126.47ms        2         
PBF Scan             Class Filter              128.74ms        2         
PBF Scan             Tourism Search            136.84ms        12        
PBF Scan             Shop Search               144.78ms        10
```

## Benchmark Results (taiwan-latest.osm.pbf, 315.47 MB)
```plain
============================================================
INDEX SIZE COMPARISON
============================================================
Scenario             Index Size      Build Time     
------------------------------------------------------------
PBF Scan             0 B (Live)      0s             
PMTiles Scan         0 B (Live)      0s             
Minimal Mode         54.51 MB        43.45s         
No Geo               498.58 MB       228.77s        
Nodes Only           720.99 MB       143.82s        
Centroids (Simple)   2.73 GB         622.79s        
Wiki Redirects       3.10 GB         635.85s        
Cached Searches      3.27 GB         545.98s        
Addresses            3.48 GB         612.00s        
Representative Pts   3.51 GB         606.79s        
Bounding Boxes       4.36 GB         849.38s        

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Cached Searches      Class Filter              8.13µs          1         
Cached Searches      Subtype Filter            16.54µs         1         
Cached Searches      Combined (Fuzzy+Class)    25.37µs         1         
Cached Searches      Fuzzy Search              49.47µs         38        
PMTiles Scan         BBox Search               62.81µs         0         
Addresses            Address Match             65.48µs         0         
PMTiles Scan         Radius Search             101.37µs        0         
PMTiles Scan         Subtype Filter            103.50µs        0         
Cached Searches      Prefix Search             109.33µs        167       
PMTiles Scan         Tourism Search            126.05µs        0         
PMTiles Scan         Prefix Search             131.08µs        0         
PMTiles Scan         Class Filter              131.73µs        0         
PMTiles Scan         Shop Search               136.10µs        0         
PMTiles Scan         Fuzzy Search              137.16µs        0         
PMTiles Scan         Combined (Fuzzy+Class)    141.88µs        0         
Nodes Only           Subtype Filter            220.64µs        1         
No Geo               Subtype Filter            241.07µs        1         
No Geo               Class Filter              291.70µs        1         
PMTiles Scan         Basic Search              326.41µs        0         
Nodes Only           Class Filter              326.79µs        1         
Wiki Redirects       Subtype Filter            413.43µs        1         
Cached Searches      Basic Search              439.20µs        37        
Cached Searches      Shop Search               467.94µs        1424      
Cached Searches      Tourism Search            477.26µs        687       
Centroids (Simple)   Subtype Filter            498.03µs        1         
Wiki Redirects       Class Filter              564.15µs        1         
Centroids (Simple)   Class Filter              565.02µs        1         
Cached Searches      BBox Search               690.02µs        3751      
Bounding Boxes       Subtype Filter            725.23µs        1         
Bounding Boxes       Class Filter              1.13ms          1         
Cached Searches      Radius Search             1.21ms          8119      
Minimal Mode         Basic Search              1.44ms          30        
Addresses            Subtype Filter            1.64ms          1         
Representative Pts   Class Filter              1.87ms          1         
Representative Pts   Subtype Filter            2.04ms          1         
Addresses            Class Filter              2.07ms          1         
Nodes Only           Basic Search              2.64ms          32        
No Geo               Combined (Fuzzy+Class)    2.68ms          1         
No Geo               Basic Search              2.77ms          37        
Nodes Only           Combined (Fuzzy+Class)    2.85ms          1         
Minimal Mode         Fuzzy Search              3.31ms          31        
Wiki Redirects       Combined (Fuzzy+Class)    3.34ms          1         
Minimal Mode         Prefix Search             4.15ms          144       
Centroids (Simple)   Combined (Fuzzy+Class)    4.28ms          1         
Nodes Only           Fuzzy Search              4.70ms          33        
No Geo               Fuzzy Search              4.84ms          38        
Addresses            Basic Search              5.01ms          37        
Centroids (Simple)   Basic Search              5.26ms          37        
Bounding Boxes       Combined (Fuzzy+Class)    5.55ms          1         
Bounding Boxes       Basic Search              6.44ms          37        
Nodes Only           Prefix Search             6.73ms          147       
Wiki Redirects       Basic Search              6.82ms          37        
No Geo               Prefix Search             7.01ms          167       
Representative Pts   Combined (Fuzzy+Class)    7.24ms          1         
Addresses            Combined (Fuzzy+Class)    8.85ms          1         
Centroids (Simple)   Fuzzy Search              9.15ms          38        
Representative Pts   Basic Search              9.51ms          37        
Wiki Redirects       Fuzzy Search              10.12ms         38        
Bounding Boxes       Fuzzy Search              10.36ms         38        
Centroids (Simple)   Prefix Search             11.91ms         167       
Wiki Redirects       Prefix Search             12.64ms         167       
Addresses            Fuzzy Search              14.54ms         38        
Representative Pts   Fuzzy Search              15.41ms         38        
Bounding Boxes       Prefix Search             16.43ms         167       
Nodes Only           Tourism Search            21.29ms         217       
Representative Pts   Prefix Search             22.82ms         167       
Addresses            Prefix Search             24.00ms         167       
Nodes Only           Shop Search               30.32ms         1361      
Nodes Only           BBox Search               54.96ms         1587      
Wiki Redirects       Shop Search               66.03ms         1424      
Wiki Redirects       Tourism Search            67.14ms         687       
No Geo               Shop Search               70.27ms         1424      
Centroids (Simple)   Shop Search               71.05ms         1424      
Centroids (Simple)   Tourism Search            72.21ms         687       
Bounding Boxes       Shop Search               73.48ms         1424      
No Geo               Tourism Search            74.39ms         687       
Bounding Boxes       Tourism Search            74.53ms         687       
Representative Pts   Shop Search               77.08ms         1424      
Nodes Only           Radius Search             82.87ms         3293      
Addresses            Shop Search               83.00ms         1424      
Representative Pts   Tourism Search            83.12ms         687       
Addresses            Tourism Search            86.41ms         687       
Wiki Redirects       BBox Search               136.97ms        3751      
Representative Pts   BBox Search               144.06ms        3748      
Centroids (Simple)   BBox Search               146.77ms        3751      
Addresses            BBox Search               148.56ms        3751      
Wiki Redirects       Radius Search             216.45ms        8119      
Centroids (Simple)   Radius Search             231.89ms        8119      
Representative Pts   Radius Search             235.84ms        8116      
Addresses            Radius Search             256.34ms        8119      
PBF Scan             Shop Search               409.54ms        50        
PBF Scan             Tourism Search            549.54ms        50        
PBF Scan             Prefix Search             762.36ms        50        
Bounding Boxes       Radius Search             1.25s           3310      
Bounding Boxes       BBox Search               1.27s           1594      
PBF Scan             Combined (Fuzzy+Class)    24.30s          1         
PBF Scan             Fuzzy Search              24.32s          38        
PBF Scan             Subtype Filter            24.41s          1         
PBF Scan             Class Filter              24.47s          1         
PBF Scan             Basic Search              24.55s          38
```

## Features

- Multi-Interface Search: Search via high-performance Bleve index, direct PBF scan    , or PMTiles.
- Rich Metadata: Support for `phone`, `opening_hours`, `wheelchair` accessibility, and detailed classification.
- Enhanced Results: Names are automatically enhanced with `brand`, `operator`, `religion`, or `denomination` (e.g., "St. Mary's (Catholic)").
- Advanced Address Search: Support for house numbers, streets, cities, postcodes, and even `floor`, `unit`, and `level`.
- Spatial Filters: Radius search, bounding box filters, and precise intersection checks.
- On-the-fly Classification: Sophisticated POI classification based on an extensible ontology.

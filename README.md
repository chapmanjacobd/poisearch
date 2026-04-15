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
| `key`, `value` | string | `amenity`, `restaurant` | Filter by primary classification |
| `keys`, `values` | string | `amenity,shop` | Comma-separated multi-value filters |

### Supported Tags

| Category | Searchable Keys | Returned Fields |
| :--- | :--- | :--- |
| Names | `name`, `alt_name`, `short_name`, `old_name`, `brand`, `operator`, `name:lang` | Full name set + enhanced name |
| Classification | `amenity`, `shop`, `tourism`, `leisure`, `place`, `highway`, etc. | `key`, `value`, `keys`, `values` |
| Address | `addr:street`, `addr:housenumber`, `addr:city`, `addr:postcode`, `addr:country`, `addr:floor`, `addr:unit`, `level` | Full address set |
| Metadata | `phone`, `opening_hours`, `wheelchair`, `wikidata`, `wikipedia` | `phone`, `opening_hours`, `wheelchair`, `importance` |

## Options

`config.toml` defines both index creation time and runtime options. There are three main search strategies, each with different tradeoffs:

| Feature/Tag | Bleve Index | PBF (`--mode=pbf`) | PMTiles (`--mode=pmtiles`) |
| :--- | :--- | :--- | :--- |
| Searchable Fields | Explicitly mapped fields (Name, Address, Metadata, Key). Fast & Scalable. | Full Tag Set. Matches against all OSM tags via arbitrary data fallback. | Limited by PMTiles schema (default: OpenMapTiles). |
| Performance | High. Optimized for large datasets and complex queries. | Low. Linear scan of the PBF file. Best for small files. | Medium. Spatial indexing allows fast lookups without a full index. |
| Address Support | Strong. Indexed `addr:*` tags including floor, unit, and level. | Strong. Direct access to all address tags in the PBF. | Weak. Most address tags are stripped by default OMT schema. |
| Classification | Static. Pre-calculated at index time for maximum speed. | Dynamic. Re-calculates classification on every scan. | Heuristic. Re-maps generic OMT keys back to OSM tags. |
| Custom Tags | Requires updating `mapping.go` and re-indexing. | High. Instant support for any tag via `TagMap()`. | Very Low. Limited to what `planetiler` preserves. |

## Benchmark Results (liechtenstein-latest.osm.pbf, 3.19 MB)
```plain
============================================================
INDEX SIZE COMPARISON
============================================================
Scenario             Index Size      Build Time     
------------------------------------------------------------
PBF Scan             0 B (Live)      0s             
PMTiles Scan         0 B (Live)      0s             
Minimal Mode         1016.57 KB      469.57ms       
No Geo               13.22 MB        4.88s          
Nodes Only           25.12 MB        4.12s          
Addresses            58.05 MB        10.81s         
Representative Pts   60.44 MB        10.81s         
Centroids (Simple)   61.74 MB        10.72s         
Wiki Redirects       63.92 MB        10.80s         
Bounding Boxes       98.19 MB        13.50s         

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Cached Searches      Combined (Fuzzy+Key)      6.48µs          1         
Cached Searches      Shop Search               7.77µs          10        
Cached Searches      Key Filter                10.91µs         183       
Cached Searches      Tourism Search            11.13µs         12        
Cached Searches      Value Filter              11.20µs         183       
Cached Searches      Fuzzy Search              13.98µs         183       
Cached Searches      Basic Search              16.03µs         183       
Cached Searches      Prefix Search             16.58µs         186       
Cached Searches      BBox Search               74.49µs         2072      
Cached Searches      Radius Search             86.60µs         3799      
Centroids (Simple)   Combined (Fuzzy+Key)      489.04µs        1         
No Geo               Combined (Fuzzy+Key)      497.35µs        1         
Minimal Mode         Basic Search              519.93µs        126       
Bounding Boxes       Combined (Fuzzy+Key)      530.19µs        1         
Nodes Only           Combined (Fuzzy+Key)      590.68µs        1         
Representative Pts   Combined (Fuzzy+Key)      595.05µs        1         
Wiki Redirects       Combined (Fuzzy+Key)      606.93µs        1         
Minimal Mode         Prefix Search             628.38µs        127       
Addresses            Combined (Fuzzy+Key)      637.73µs        1         
Minimal Mode         Fuzzy Search              785.53µs        126       
Nodes Only           Tourism Search            981.28µs        7         
No Geo               Shop Search               1.04ms          10        
Addresses            Shop Search               1.06ms          10        
Centroids (Simple)   Shop Search               1.07ms          10        
Wiki Redirects       Shop Search               1.07ms          10        
Nodes Only           Shop Search               1.13ms          10        
Representative Pts   Shop Search               1.13ms          10        
Bounding Boxes       Shop Search               1.23ms          10        
Nodes Only           Key Filter                1.23ms          126       
Nodes Only           Basic Search              1.24ms          126       
Nodes Only           Value Filter              1.33ms          126       
Centroids (Simple)   Tourism Search            1.64ms          12        
Representative Pts   Tourism Search            1.65ms          12        
Nodes Only           Prefix Search             1.67ms          127       
Addresses            Address Match             1.68ms          7         
No Geo               Key Filter                1.68ms          183       
No Geo               Value Filter              1.71ms          183       
Centroids (Simple)   Value Filter              1.72ms          183       
No Geo               Tourism Search            1.77ms          12        
Addresses            Tourism Search            1.81ms          12        
No Geo               Basic Search              1.81ms          183       
Representative Pts   Value Filter              1.82ms          183       
Nodes Only           Fuzzy Search              1.88ms          126       
Wiki Redirects       Tourism Search            1.88ms          12        
Representative Pts   Key Filter                1.90ms          183       
Centroids (Simple)   Key Filter                1.98ms          183       
Representative Pts   Basic Search              1.99ms          183       
Bounding Boxes       Key Filter                2.00ms          183       
Bounding Boxes       Tourism Search            2.01ms          12        
Centroids (Simple)   Basic Search              2.03ms          183       
Wiki Redirects       Value Filter              2.05ms          183       
No Geo               Prefix Search             2.05ms          186       
Addresses            Basic Search              2.12ms          183       
Wiki Redirects       Key Filter                2.13ms          183       
Addresses            Key Filter                2.14ms          183       
No Geo               Fuzzy Search              2.18ms          183       
Addresses            Value Filter              2.19ms          183       
Wiki Redirects       Basic Search              2.20ms          183       
Bounding Boxes       Basic Search              2.29ms          183       
Centroids (Simple)   Fuzzy Search              2.40ms          183       
Centroids (Simple)   Prefix Search             2.41ms          186       
Representative Pts   Prefix Search             2.49ms          186       
Wiki Redirects       Prefix Search             2.52ms          186       
Bounding Boxes       Value Filter              2.52ms          183       
Bounding Boxes       Prefix Search             2.52ms          186       
Representative Pts   Fuzzy Search              2.55ms          183       
Addresses            Prefix Search             2.67ms          186       
Wiki Redirects       Fuzzy Search              2.72ms          183       
Addresses            Fuzzy Search              2.89ms          183       
Bounding Boxes       Fuzzy Search              3.05ms          183       
PMTiles Scan         Radius Search             6.69ms          50        
Nodes Only           BBox Search               7.25ms          969       
PMTiles Scan         BBox Search               8.70ms          50        
Nodes Only           Radius Search             8.76ms          1617      
Centroids (Simple)   BBox Search               15.37ms         2072      
PMTiles Scan         Basic Search              15.65ms         0         
Wiki Redirects       BBox Search               15.67ms         2072      
Addresses            BBox Search               16.42ms         2072      
Representative Pts   BBox Search               16.55ms         2071      
PMTiles Scan         Tourism Search            17.09ms         0         
PMTiles Scan         Shop Search               17.57ms         0         
PMTiles Scan         Value Filter              17.71ms         0         
PMTiles Scan         Combined (Fuzzy+Key)      18.02ms         0         
PMTiles Scan         Prefix Search             18.56ms         0         
Representative Pts   Radius Search             18.80ms         3797      
Wiki Redirects       Radius Search             19.16ms         3799      
Centroids (Simple)   Radius Search             19.71ms         3799      
PMTiles Scan         Key Filter                19.82ms         0         
Addresses            Radius Search             19.92ms         3799      
PMTiles Scan         Fuzzy Search              20.01ms         0         
Bounding Boxes       BBox Search               62.60ms         984       
Bounding Boxes       Radius Search             64.42ms         1636      
PBF Scan             Fuzzy Search              100.83ms        50        
PBF Scan             Basic Search              107.35ms        50        
PBF Scan             Prefix Search             112.59ms        50        
PBF Scan             Shop Search               339.77ms        10        
PBF Scan             Key Filter                346.58ms        2         
PBF Scan             Value Filter              357.44ms        1         
PBF Scan             Combined (Fuzzy+Key)      372.24ms        2         
PBF Scan             Tourism Search            375.17ms        12
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
Cached Searches      Key Filter                8.13µs          1         
Cached Searches      Value Filter              16.54µs         1         
Cached Searches      Combined (Fuzzy+Key)      25.37µs         1         
Cached Searches      Fuzzy Search              49.47µs         38        
PMTiles Scan         BBox Search               62.81µs         0         
Addresses            Address Match             65.48µs         0         
PMTiles Scan         Radius Search             101.37µs        0         
PMTiles Scan         Value Filter              103.50µs        0         
Cached Searches      Prefix Search             109.33µs        167       
PMTiles Scan         Tourism Search            126.05µs        0         
PMTiles Scan         Prefix Search             131.08µs        0         
PMTiles Scan         Key Filter                131.73µs        0         
PMTiles Scan         Shop Search               136.10µs        0         
PMTiles Scan         Fuzzy Search              137.16µs        0         
PMTiles Scan         Combined (Fuzzy+Key)      141.88µs        0         
Nodes Only           Value Filter              220.64µs        1         
No Geo               Value Filter              241.07µs        1         
No Geo               Key Filter                291.70µs        1         
PMTiles Scan         Basic Search              326.41µs        0         
Nodes Only           Key Filter                326.79µs        1         
Wiki Redirects       Value Filter              413.43µs        1         
Cached Searches      Basic Search              439.20µs        37        
Cached Searches      Shop Search               467.94µs        1424      
Cached Searches      Tourism Search            477.26µs        687       
Centroids (Simple)   Value Filter              498.03µs        1         
Wiki Redirects       Key Filter                564.15µs        1         
Centroids (Simple)   Key Filter                565.02µs        1         
Cached Searches      BBox Search               690.02µs        3751      
Bounding Boxes       Value Filter              725.23µs        1         
Bounding Boxes       Key Filter                1.13ms          1         
Cached Searches      Radius Search             1.21ms          8119      
Minimal Mode         Basic Search              1.44ms          30        
Addresses            Value Filter              1.64ms          1         
Representative Pts   Key Filter                1.87ms          1         
Representative Pts   Value Filter              2.04ms          1         
Addresses            Key Filter                2.07ms          1         
Nodes Only           Basic Search              2.64ms          32        
No Geo               Combined (Fuzzy+Key)      2.68ms          1         
No Geo               Basic Search              2.77ms          37        
Nodes Only           Combined (Fuzzy+Key)      2.85ms          1         
Minimal Mode         Fuzzy Search              3.31ms          31        
Wiki Redirects       Combined (Fuzzy+Key)      3.34ms          1         
Minimal Mode         Prefix Search             4.15ms          144       
Centroids (Simple)   Combined (Fuzzy+Key)      4.28ms          1         
Nodes Only           Fuzzy Search              4.70ms          33        
No Geo               Fuzzy Search              4.84ms          38        
Addresses            Basic Search              5.01ms          37        
Centroids (Simple)   Basic Search              5.26ms          37        
Bounding Boxes       Combined (Fuzzy+Key)      5.55ms          1         
Bounding Boxes       Basic Search              6.44ms          37        
Nodes Only           Prefix Search             6.73ms          147       
Wiki Redirects       Basic Search              6.82ms          37        
No Geo               Prefix Search             7.01ms          167       
Representative Pts   Combined (Fuzzy+Key)      7.24ms          1         
Addresses            Combined (Fuzzy+Key)      8.85ms          1         
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
PBF Scan             Combined (Fuzzy+Key)      24.30s          1         
PBF Scan             Fuzzy Search              24.32s          38        
PBF Scan             Value Filter              24.41s          1         
PBF Scan             Key Filter                24.47s          1         
PBF Scan             Basic Search              24.55s          38
```

## Features

- Multi-Interface Search: Search via high-performance Bleve index, direct PBF scan    , or PMTiles.
- Rich Metadata: Support for `phone`, `opening_hours`, `wheelchair` accessibility, and detailed classification.
- Enhanced Results: Names are automatically enhanced with `brand`, `operator`, `religion`, or `denomination` (e.g., "St. Mary's (Catholic)").
- Advanced Address Search: Support for house numbers, streets, cities, postcodes, and even `floor`, `unit`, and `level`.
- Spatial Filters: Radius search, bounding box filters, and precise intersection checks.
- On-the-fly Classification: Sophisticated POI classification based on an extensible ontology.

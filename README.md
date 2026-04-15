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

## Bleve vs PBF vs PMTiles-based search

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
Minimal Mode         1001.28 KB      492.11ms       
No Geo               12.16 MB        7.47s          
Nodes Only           34.29 MB        4.90s          
Addresses            57.00 MB        11.23s         
Wiki Redirects       60.44 MB        10.67s         
Centroids (Simple)   60.44 MB        12.89s         
Representative Pts   60.48 MB        10.88s         
Bounding Boxes       97.74 MB        14.29s         

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Nodes Only           Combined (Fuzzy+Key)      594.58µs        1         
Wiki Redirects       Combined (Fuzzy+Key)      601.69µs        1         
Addresses            Combined (Fuzzy+Key)      657.88µs        1         
Centroids (Simple)   Combined (Fuzzy+Key)      677.68µs        1         
No Geo               Combined (Fuzzy+Key)      678.44µs        1         
Bounding Boxes       Combined (Fuzzy+Key)      694.52µs        1         
Representative Pts   Combined (Fuzzy+Key)      758.96µs        1         
Minimal Mode         Basic Search              943.38µs        126       
Representative Pts   Shop Search               1.12ms          10        
Nodes Only           Shop Search               1.20ms          10        
Bounding Boxes       Shop Search               1.24ms          10        
Nodes Only           Tourism Search            1.27ms          7         
Addresses            Shop Search               1.32ms          10        
Centroids (Simple)   Shop Search               1.36ms          10        
Wiki Redirects       Shop Search               1.41ms          10        
No Geo               Shop Search               1.51ms          10        
Addresses            Address Match             1.73ms          7         
Nodes Only           Value Filter              1.81ms          126       
Minimal Mode         Fuzzy Search              1.83ms          126       
Nodes Only           Key Filter                1.83ms          126       
Bounding Boxes       Tourism Search            1.90ms          12        
Representative Pts   Tourism Search            1.91ms          12        
No Geo               Tourism Search            1.94ms          12        
Minimal Mode         Prefix Search             1.96ms          127       
Wiki Redirects       Tourism Search            1.98ms          12        
Centroids (Simple)   Tourism Search            2.07ms          12        
Nodes Only           Basic Search              2.17ms          126       
Addresses            Tourism Search            2.23ms          12        
No Geo               Key Filter                2.49ms          183       
No Geo               Value Filter              2.56ms          183       
Representative Pts   Value Filter              2.57ms          183       
No Geo               Basic Search              2.57ms          183       
Representative Pts   Key Filter                2.70ms          183       
Wiki Redirects       Value Filter              2.78ms          183       
Bounding Boxes       Value Filter              2.78ms          183       
Wiki Redirects       Key Filter                2.81ms          183       
Centroids (Simple)   Value Filter              2.94ms          183       
Centroids (Simple)   Key Filter                2.94ms          183       
Representative Pts   Basic Search              3.06ms          183       
Bounding Boxes       Key Filter                3.14ms          183       
Addresses            Key Filter                3.15ms          183       
Addresses            Value Filter              3.27ms          183       
Addresses            Basic Search              3.30ms          183       
Wiki Redirects       Basic Search              3.36ms          183       
Centroids (Simple)   Basic Search              3.54ms          183       
Nodes Only           Prefix Search             3.58ms          147       
Bounding Boxes       Basic Search              3.63ms          183       
Nodes Only           Fuzzy Search              3.87ms          146       
No Geo               Prefix Search             4.52ms          208       
Representative Pts   Prefix Search             4.79ms          208       
Wiki Redirects       Prefix Search             4.81ms          208       
No Geo               Fuzzy Search              4.87ms          205       
Wiki Redirects       Fuzzy Search              5.17ms          205       
Representative Pts   Fuzzy Search              5.21ms          205       
Centroids (Simple)   Prefix Search             5.34ms          208       
Centroids (Simple)   Fuzzy Search              5.49ms          205       
Bounding Boxes       Prefix Search             5.62ms          208       
Addresses            Prefix Search             5.80ms          208       
Addresses            Fuzzy Search              5.86ms          205       
Bounding Boxes       Fuzzy Search              5.91ms          205       
PMTiles Scan         BBox Search               12.11ms         632       
PMTiles Scan         Fuzzy Search              21.91ms         114       
PMTiles Scan         Key Filter                22.13ms         4         
PMTiles Scan         Combined (Fuzzy+Key)      22.39ms         4         
PMTiles Scan         Value Filter              22.62ms         4         
Nodes Only           BBox Search               24.56ms         969       
PMTiles Scan         Prefix Search             24.89ms         114       
PMTiles Scan         Basic Search              24.91ms         114       
PMTiles Scan         Shop Search               25.13ms         5         
PMTiles Scan         Tourism Search            26.69ms         5         
PMTiles Scan         Radius Search             26.99ms         1476      
Nodes Only           Radius Search             35.52ms         1617      
Representative Pts   BBox Search               44.50ms         2071      
Wiki Redirects       BBox Search               44.97ms         2072      
Centroids (Simple)   BBox Search               45.79ms         2072      
Addresses            BBox Search               49.73ms         2072      
Representative Pts   Radius Search             71.00ms         3797      
Wiki Redirects       Radius Search             73.06ms         3799      
Centroids (Simple)   Radius Search             76.78ms         3799      
Addresses            Radius Search             81.08ms         3799      
Bounding Boxes       BBox Search               89.60ms         984       
Bounding Boxes       Radius Search             98.06ms         1636      
PBF Scan             Shop Search               372.68ms        10        
PBF Scan             Tourism Search            379.90ms        12        
PBF Scan             Combined (Fuzzy+Key)      388.09ms        2         
PBF Scan             Prefix Search             391.87ms        809       
PBF Scan             Basic Search              394.39ms        760       
PBF Scan             Value Filter              397.97ms        1         
PBF Scan             Key Filter                402.00ms        2         
PBF Scan             Fuzzy Search              428.47ms        760
```

## Benchmark Results (taiwan-latest.osm.pbf, 308.27 MB)
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
Representative Pts   3.51 GB         606.79s        
Bounding Boxes       4.36 GB         849.38s        
Addresses            11.66 GB        3062.26s

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Cached Searches      Key Filter                8.13µs          1         
Cached Searches      Value Filter              16.54µs         1         
Cached Searches      Combined (Fuzzy+Key)      25.37µs         1         
Cached Searches      Fuzzy Search              49.47µs         38        
PMTiles Scan         BBox Search               59.16ms         1465         
PMTiles Scan         Radius Search             175.34ms        4124         
PMTiles Scan         Value Filter              164.46ms        4         
Cached Searches      Prefix Search             109.33µs        167       
PMTiles Scan         Tourism Search            162.46ms        1         
PMTiles Scan         Prefix Search             173.06ms        176         
PMTiles Scan         Key Filter                171.95ms        4         
PMTiles Scan         Shop Search               163.70ms        30         
PMTiles Scan         Fuzzy Search              187.95ms        115         
PMTiles Scan         Combined (Fuzzy+Key)      161.62ms        4         
Nodes Only           Value Filter              220.64µs        1         
No Geo               Value Filter              241.07µs        1         
No Geo               Key Filter                291.70µs        1         
PMTiles Scan         Basic Search              197.24ms        115         
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
Representative Pts   Key Filter                1.87ms          1         
Representative Pts   Value Filter              2.04ms          1         
Addresses            Key Filter                2.49ms          44
Addresses            Value Filter              2.53ms          44
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
Addresses            Combined (Fuzzy+Key)      4.89ms          10
Addresses            Basic Search              5.00ms          44
Centroids (Simple)   Basic Search              5.26ms          37        
Bounding Boxes       Combined (Fuzzy+Key)      5.55ms          1         
Bounding Boxes       Basic Search              6.44ms          37        
Nodes Only           Prefix Search             6.73ms          147       
Wiki Redirects       Basic Search              6.82ms          37        
No Geo               Prefix Search             7.01ms          167       
Representative Pts   Combined (Fuzzy+Key)      7.24ms          1         
Centroids (Simple)   Fuzzy Search              9.15ms          38        
Representative Pts   Basic Search              9.51ms          37        
Wiki Redirects       Fuzzy Search              10.12ms         38        
Bounding Boxes       Fuzzy Search              10.36ms         38        
Centroids (Simple)   Prefix Search             11.91ms         167       
Wiki Redirects       Prefix Search             12.64ms         167       
Representative Pts   Fuzzy Search              15.41ms         38        
Bounding Boxes       Prefix Search             16.43ms         167       
Nodes Only           Tourism Search            21.29ms         217       
Representative Pts   Prefix Search             22.82ms         167       
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
Representative Pts   Tourism Search            83.12ms         687       
Addresses            Fuzzy Search              85.56ms         1824
Wiki Redirects       BBox Search               136.97ms        3751      
Representative Pts   BBox Search               144.06ms        3748      
Centroids (Simple)   BBox Search               146.77ms        3751      
Wiki Redirects       Radius Search             216.45ms        8119      
Centroids (Simple)   Radius Search             231.89ms        8119      
Representative Pts   Radius Search             235.84ms        8116      
Addresses            Radius Search             256.34ms        8119      
Addresses            Prefix Search             284.35ms        10384
Addresses            Tourism Search            332.29ms        689
Addresses            Shop Search               348.53ms        1424
Addresses            Address Match             377.58ms        3927
PBF Scan             Shop Search               409.54ms        50        
PBF Scan             Tourism Search            549.54ms        50        
Addresses            BBox Search               690.45ms        26275
PBF Scan             Prefix Search             762.36ms        50        
Addresses            Radius Search             954.42ms        55557
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

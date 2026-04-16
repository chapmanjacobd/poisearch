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
| `limit` | int | `100` | Maximum number of results to return (default: 100, max: 1000) |
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
Minimal Mode         2.02 MB         495.69ms       
No Geo               13.40 MB        6.36s          
Nodes Only           26.27 MB        4.69s          
Addresses            69.33 MB        15.98s         
Wiki Redirects       71.37 MB        13.18s         
Centroids (Simple)   71.38 MB        12.62s         
Representative Pts   71.38 MB        12.74s         
Bounding Boxes       147.35 MB       17.09s         

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Nodes Only           Combined (Fuzzy+Key)      708.43µs        4         
Addresses            Shop Search               719.41µs        10        
Nodes Only           Tourism Search            737.05µs        7         
Representative Pts   Combined (Fuzzy+Key)      787.81µs        4         
Wiki Redirects       Shop Search               832.25µs        10        
Wiki Redirects       Combined (Fuzzy+Key)      837.72µs        4         
Representative Pts   Shop Search               848.87µs        10        
No Geo               Shop Search               862.70µs        10        
Bounding Boxes       Combined (Fuzzy+Key)      862.78µs        4         
Centroids (Simple)   Shop Search               902.05µs        10        
No Geo               Combined (Fuzzy+Key)      905.54µs        4         
Addresses            Combined (Fuzzy+Key)      908.96µs        4         
Nodes Only           Shop Search               947.65µs        10        
Centroids (Simple)   Combined (Fuzzy+Key)      991.34µs        4         
Bounding Boxes       Shop Search               1.05ms          10        
Minimal Mode         Basic Search              1.08ms          179       
Centroids (Simple)   Tourism Search            1.33ms          12        
Representative Pts   Tourism Search            1.38ms          12        
Wiki Redirects       Tourism Search            1.49ms          12        
Addresses            Tourism Search            1.56ms          12        
No Geo               Tourism Search            1.58ms          12        
Bounding Boxes       Tourism Search            1.71ms          12        
Addresses            Address Match             1.96ms          26        
Nodes Only           Key Filter                2.18ms          179       
Nodes Only           Value Filter              2.24ms          179       
Nodes Only           Basic Search              2.24ms          179       
Minimal Mode         Fuzzy Search              2.31ms          179       
Minimal Mode         Prefix Search             2.63ms          179       
No Geo               Value Filter              3.42ms          341       
Wiki Redirects       Value Filter              3.42ms          341       
Representative Pts   Key Filter                3.46ms          341       
No Geo               Key Filter                3.51ms          341       
Representative Pts   Value Filter              3.52ms          341       
Addresses            Key Filter                3.61ms          341       
Wiki Redirects       Basic Search              3.63ms          341       
Centroids (Simple)   Key Filter                3.68ms          341       
Representative Pts   Basic Search              3.71ms          341       
Centroids (Simple)   Basic Search              3.78ms          341       
No Geo               Basic Search              3.80ms          341       
Addresses            Value Filter              3.82ms          341       
Wiki Redirects       Key Filter                3.95ms          341       
Centroids (Simple)   Value Filter              3.97ms          341       
Addresses            Basic Search              3.97ms          341       
Nodes Only           Prefix Search             4.22ms          213       
Nodes Only           Fuzzy Search              4.36ms          213       
Bounding Boxes       Key Filter                4.64ms          341       
Bounding Boxes       Value Filter              5.04ms          341       
Bounding Boxes       Basic Search              5.91ms          341       
No Geo               Fuzzy Search              6.71ms          384       
No Geo               Prefix Search             6.80ms          384       
Representative Pts   Prefix Search             6.86ms          384       
Wiki Redirects       Fuzzy Search              6.93ms          384       
Wiki Redirects       Prefix Search             7.07ms          384       
Centroids (Simple)   Fuzzy Search              7.13ms          384       
Addresses            Prefix Search             7.15ms          384       
Centroids (Simple)   Prefix Search             7.31ms          384       
Representative Pts   Fuzzy Search              7.48ms          384       
Addresses            Fuzzy Search              7.71ms          384       
Bounding Boxes       Prefix Search             8.60ms          384       
Bounding Boxes       Fuzzy Search              9.12ms          384       
PMTiles Scan         BBox Search               13.37ms         717       
Nodes Only           BBox Search               20.60ms         981       
PMTiles Scan         Combined (Fuzzy+Key)      21.52ms         4         
Nodes Only           Radius Search             21.93ms         1643      
PMTiles Scan         Shop Search               22.21ms         5         
PMTiles Scan         Prefix Search             22.95ms         114       
PMTiles Scan         Tourism Search            23.21ms         5         
PMTiles Scan         Key Filter                23.60ms         4         
PMTiles Scan         Value Filter              25.87ms         4         
PMTiles Scan         Basic Search              26.25ms         114       
PMTiles Scan         Fuzzy Search              26.39ms         114       
Representative Pts   BBox Search               27.79ms         2349      
Wiki Redirects       BBox Search               28.14ms         2350      
Centroids (Simple)   BBox Search               28.63ms         2350      
PMTiles Scan         Radius Search             28.94ms         1707      
Addresses            BBox Search               29.79ms         2350      
Wiki Redirects       Radius Search             30.04ms         4433      
Representative Pts   Radius Search             30.87ms         4431      
Centroids (Simple)   Radius Search             31.20ms         4433      
Addresses            Radius Search             32.90ms         4433      
Bounding Boxes       BBox Search               84.95ms         996       
Bounding Boxes       Radius Search             86.72ms         1662      
PBF Scan             Shop Search               388.59ms        10        
PBF Scan             Combined (Fuzzy+Key)      394.41ms        2         
PBF Scan             Key Filter                403.40ms        2         
PBF Scan             Tourism Search            404.25ms        12        
PBF Scan             Value Filter              410.75ms        1         
PBF Scan             Prefix Search             441.20ms        1870      
PBF Scan             Fuzzy Search              471.68ms        1818      
PBF Scan             Basic Search              495.07ms        1818
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

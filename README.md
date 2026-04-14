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

## Features

- Multi-Interface Search: Search via high-performance Bleve index, direct PBF scan, or PMTiles.
- Rich Metadata: Support for `phone`, `opening_hours`, `wheelchair` accessibility, and detailed classification.
- Enhanced Results: Names are automatically enhanced with `brand`, `operator`, `religion`, or `denomination` (e.g., "St. Mary's (Catholic)").
- Advanced Address Search: Support for house numbers, streets, cities, postcodes, and even `floor`, `unit`, and `level`.
- Spatial Filters: Radius search, bounding box filters, and precise intersection checks.
- On-the-fly Classification: Sophisticated POI classification based on an extensible ontology.

### Supported Tags

| Category | Searchable Keys | Returned Fields |
| :--- | :--- | :--- |
| Names | `name`, `alt_name`, `short_name`, `old_name`, `brand`, `operator`, `name:lang` | Full name set + enhanced name |
| Classification | `amenity`, `shop`, `tourism`, `leisure`, `place`, `highway`, etc. | `class`, `subtype`, `classes`, `subtypes` |
| Address | `addr:street`, `addr:housenumber`, `addr:city`, `addr:postcode`, `addr:country`, `addr:floor`, `addr:unit`, `level` | Full address set |
| Metadata | `phone`, `opening_hours`, `wheelchair`, `wikidata`, `wikipedia` | `phone`, `opening_hours`, `wheelchair`, `importance` |

## Options

`config.toml` defines both index creation time and runtime options. There are three main search strategies, each with different tradeoffs:

| Feature/Tag | Bleve Index | Raw PBF (`--mode=pbf`) | Raw PMTiles (`--mode=pmtiles`) |
| :--- | :--- | :--- | :--- |
| Searchable Fields | Explicitly mapped fields (Name, Address, Metadata, Class). Fast & Scalable. | Full Tag Set. Matches against all OSM tags via arbitrary data fallback. | Limited by PMTiles schema (default: OpenMapTiles). |
| Performance | High. Optimized for large datasets and complex queries. | Low. Linear scan of the PBF file. Best for small files. | Medium. Spatial indexing allows fast lookups without a full index. |
| Address Support | Strong. Indexed `addr:*` tags including floor, unit, and level. | Strong. Direct access to all address tags in the PBF. | Weak. Most address tags are stripped by default OMT schema. |
| Classification | Static. Pre-calculated at index time for maximum speed. | Dynamic. Re-calculates classification on every scan. | Heuristic. Re-maps generic OMT classes back to OSM tags. |
| Custom Tags | Requires updating `mapping.go` and re-indexing. | High. Instant support for any tag via `TagMap()`. | Very Low. Limited to what `planetiler` preserves. |

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
Leanest Mode         1.15 MB         295.05ms       
No Geo               13.07 MB        4.79s          
Nodes Only           29.93 MB        4.36s          
Addresses            59.76 MB        10.39s         
Centroids (Simple)   60.07 MB        10.57s         
Wiki Redirects       60.07 MB        10.46s         
Representative Pts   60.40 MB        10.66s         
Cached Searches      63.51 MB        10.19s         
Bounding Boxes       97.92 MB        13.96s         

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Cached Searches      Subtype Filter            1.38µs          1         
Cached Searches      Class Filter              1.58µs          1         
Cached Searches      Combined (Fuzzy+Class)    3.65µs          1         
Cached Searches      Shop Search               5.67µs          10        
Cached Searches      Basic Search              8.05µs          178       
Cached Searches      Prefix Search             9.99µs          181       
Cached Searches      Tourism Search            10.40µs         12        
Cached Searches      Fuzzy Search              10.45µs         178       
Addresses            Address Match             22.25µs         0         
Cached Searches      BBox Search               76.14µs         2067      
Cached Searches      Radius Search             87.88µs         3803      
Wiki Redirects       Class Filter              144.24µs        1         
Representative Pts   Subtype Filter            145.66µs        1         
Wiki Redirects       Subtype Filter            147.19µs        1         
No Geo               Class Filter              148.60µs        1         
Addresses            Subtype Filter            149.62µs        1         
Representative Pts   Class Filter              153.53µs        1         
Nodes Only           Subtype Filter            156.45µs        1         
Bounding Boxes       Subtype Filter            159.74µs        1         
No Geo               Subtype Filter            159.87µs        1         
Centroids (Simple)   Subtype Filter            165.71µs        1         
Addresses            Class Filter              172.78µs        1         
Centroids (Simple)   Class Filter              213.21µs        1         
Nodes Only           Class Filter              232.12µs        1         
Bounding Boxes       Class Filter              238.74µs        1         
No Geo               Combined (Fuzzy+Class)    526.95µs        1         
Leanest Mode         Basic Search              527.89µs        126       
Nodes Only           Combined (Fuzzy+Class)    560.20µs        1         
Representative Pts   Combined (Fuzzy+Class)    561.61µs        1         
Centroids (Simple)   Combined (Fuzzy+Class)    564.78µs        1         
Bounding Boxes       Combined (Fuzzy+Class)    569.11µs        1         
Addresses            Combined (Fuzzy+Class)    585.18µs        1         
Leanest Mode         Prefix Search             597.09µs        127       
Wiki Redirects       Combined (Fuzzy+Class)    608.16µs        1         
Leanest Mode         Fuzzy Search              750.07µs        126       
Representative Pts   Shop Search               981.57µs        10        
Bounding Boxes       Shop Search               1.05ms          10        
Wiki Redirects       Shop Search               1.13ms          10        
No Geo               Shop Search               1.15ms          10        
Nodes Only           Tourism Search            1.15ms          7         
Centroids (Simple)   Shop Search               1.18ms          10        
Addresses            Shop Search               1.22ms          10        
Nodes Only           Shop Search               1.29ms          10        
Nodes Only           Basic Search              1.45ms          126       
Centroids (Simple)   Tourism Search            1.76ms          12        
PMTiles Scan         Radius Search             1.77ms          50        
Nodes Only           Prefix Search             1.78ms          127       
Bounding Boxes       Tourism Search            1.79ms          12        
PMTiles Scan         BBox Search               1.80ms          50        
No Geo               Basic Search              1.80ms          178       
Wiki Redirects       Tourism Search            1.84ms          12        
No Geo               Tourism Search            1.85ms          12        
Representative Pts   Tourism Search            1.88ms          12        
Addresses            Tourism Search            1.93ms          12        
Nodes Only           Fuzzy Search              1.94ms          126       
Wiki Redirects       Basic Search              1.97ms          178       
Representative Pts   Basic Search              2.05ms          178       
Addresses            Basic Search              2.14ms          178       
Centroids (Simple)   Basic Search              2.16ms          178       
No Geo               Prefix Search             2.16ms          181       
Addresses            Prefix Search             2.19ms          181       
Wiki Redirects       Prefix Search             2.27ms          181       
Addresses            Fuzzy Search              2.29ms          178       
No Geo               Fuzzy Search              2.30ms          178       
Bounding Boxes       Basic Search              2.32ms          178       
Representative Pts   Prefix Search             2.34ms          181       
Wiki Redirects       Fuzzy Search              2.46ms          178       
Bounding Boxes       Prefix Search             2.49ms          181       
Representative Pts   Fuzzy Search              2.52ms          178       
Centroids (Simple)   Prefix Search             2.59ms          181       
Bounding Boxes       Fuzzy Search              2.69ms          178       
Centroids (Simple)   Fuzzy Search              2.87ms          178       
PMTiles Scan         Basic Search              5.17ms          50        
PMTiles Scan         Prefix Search             5.23ms          50        
PMTiles Scan         Fuzzy Search              5.45ms          50        
PMTiles Scan         Tourism Search            6.20ms          3         
PMTiles Scan         Combined (Fuzzy+Class)    6.87ms          4         
PMTiles Scan         Class Filter              7.40ms          4         
PMTiles Scan         Shop Search               7.47ms          5         
Nodes Only           BBox Search               7.66ms          969       
PMTiles Scan         Subtype Filter            8.15ms          4         
Nodes Only           Radius Search             9.54ms          1617      
Representative Pts   BBox Search               14.85ms         2066      
Centroids (Simple)   BBox Search               15.07ms         2067      
Wiki Redirects       BBox Search               15.12ms         2067      
Addresses            BBox Search               15.30ms         2067      
Wiki Redirects       Radius Search             18.89ms         3803      
Centroids (Simple)   Radius Search             19.38ms         3803      
Representative Pts   Radius Search             19.51ms         3802      
Addresses            Radius Search             19.77ms         3803      
Raw PBF Scan         Prefix Search             31.78ms         50        
Raw PBF Scan         Fuzzy Search              37.64ms         50        
Raw PBF Scan         Basic Search              37.76ms         50        
Bounding Boxes       BBox Search               61.84ms         983       
Bounding Boxes       Radius Search             64.68ms         1635      
Raw PBF Scan         Shop Search               109.59ms        10        
Raw PBF Scan         Class Filter              115.07ms        1         
Raw PBF Scan         Tourism Search            118.41ms        12        
Raw PBF Scan         Combined (Fuzzy+Class)    119.00ms        1         
Raw PBF Scan         Subtype Filter            122.68ms        1
```

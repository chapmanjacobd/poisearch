# poisearch 🥡

A lightweight (low RAM, medium disk) [POI](https://en.wikipedia.org/wiki/Poi_(food)) search engine for OpenStreetMap data

## Bleve vs PBF vs PMTiles-based search

`config.toml` defines both index creation time and runtime options. There are three main search strategies, each with different tradeoffs:

| Feature/Tag | Bleve Index | PBF | PMTiles |
| :--- | :--- | :--- | :--- |
| Searchable Fields | Explicitly mapped fields (Name, Address, Metadata, Key). Fast & Scalable. | Full Tag Set. Matches against all OSM tags via arbitrary data fallback. | Limited by PMTiles schema (default: OpenMapTiles). |
| Query Features | Multi-interpretation, `near` queries, fuzzy/prefix matching. | `near` queries, fuzzy/prefix matching, full-tag fallback. | `near` queries, fuzzy/prefix matching, optional `exact_match` geometry filtering. |
| Performance | High. Optimized for large datasets and complex queries. | Low. Linear scan of the PBF file. Best for small files. | Medium. Spatial indexing allows fast lookups without a full index. |
| Address Support | Strong. Indexed `addr:*` tags including floor, unit, and level. | Strong. Direct access to all address tags in the PBF. | Weak. Most address tags are stripped by default OMT schema. |
| Classification | Static. Pre-calculated at index time for maximum speed. | Dynamic. Re-calculates classification on every scan. | Heuristic. Re-maps generic OMT keys back to OSM tags. |
| Custom Tags | Requires updating `mapping.go` and re-indexing. | High. Instant support for any tag via `TagMap()`. | Very Low. Limited to what `planetiler` preserves. |

## Backend Setup

`poisearch` supports three different search backends. Choose the one that best fits your needs:

### 1. Bleve Backend Setup (High Performance)
Recommended for most use cases. It uses a pre-built Bleve index for fast, scalable searching with full support for all features.

1. Pre-process OSM PBF:
   `poisearch` requires node locations to be added to ways.
   ```sh
   osmium add-locations-to-ways input.osm.pbf -o processed.osm.pbf
   ```
   To reduce index size, you can filter for specific tags first:
   ```sh
   osmium tags-filter processed.osm.pbf n/place n/amenity n/highway -o filtered.osm.pbf
   ```

2. Configure:
   Copy `config.example.toml` to `config.toml` and ensure `index_paths` points to your desired index location:
   ```toml
   index_paths = ["pois.bleve"]
   ```

3. Build the Index:
   ```sh
   go run ./cmd/poisearch --config config.toml build processed.osm.pbf
   ```

4. Serve:
   ```sh
   go run ./cmd/poisearch --config config.toml serve
   ```

### 2. PMTiles Backend Setup (Spatial Index)
Ideal for large datasets (e.g., entire countries) where you want to minimize RAM usage and avoid a lengthy indexing process. It uses `.pmtiles` files which are spatially indexed.

1. Prepare PMTiles:
   Use the provided script to generate a PMTiles file using `planetiler`:
   ```sh
   ./scripts/pbf_to_pmtiles.sh liechtenstein
   ```

2. Configure:
   Add your PMTiles file to `pmtiles_paths` in `config.toml`:
   ```toml
   pmtiles_paths = ["liechtenstein.pmtiles"]
   ```

3. Serve:
   ```sh
   go run ./cmd/poisearch --config config.toml serve
   ```
   Access via API by adding `?mode=pmtiles` to your query.

### 3. PBF Backend Setup (Direct Search)
A "live" search mode that scans the PBF file directly. No index is needed, but performance is lower (linear scan). Best for very small areas or debugging.

1. Pre-process OSM PBF:
   ```sh
   osmium add-locations-to-ways input.osm.pbf -o processed.osm.pbf
   ```

2. Configure:
   Add your PBF file to `pbf_paths` in `config.toml`:
   ```toml
   pbf_paths = ["processed.osm.pbf"]
   ```

3. Serve:
   ```sh
   go run ./cmd/poisearch --config config.toml serve
   ```
   Access via API by adding `?mode=pbf` to your query.

## Usage

### Search Examples

```sh
# Default search (uses first available index)
curl "http://localhost:9889/search?q=Berlin&limit=5"

# Spatial search
curl "http://localhost:9889/search?q=Restaurant&lat=52.52&lon=13.40&radius=1000m"

# Natural-language "near" search
curl "http://localhost:9889/search?q=pizza%20near%20Vaduz"

# Force a specific mode
curl "http://localhost:9889/search?q=museum&mode=pmtiles"
curl "http://localhost:9889/search?q=museum&mode=pbf"

# Filter by metadata or classification
curl "http://localhost:9889/search?wheelchair=yes&city=Berlin"
curl "http://localhost:9889/search?amenity=restaurant"
```

## API Documentation

The following query parameters are supported on `/search`:

| Parameter | Type | Example | Description |
| :--- | :--- | :--- | :--- |
| `q` | string | `Berlin` | Search query string |
| `mode` | string | `pbf` | "pbf" or "pmtiles" for live search against those file types (skips index) |
| `index` | string | `pois` | Name of the index/file to search (base name of file without extension) |
| `format` | string | `text` | "text" for flat key-value response (UNIX-pipe friendly) |
| `lat`, `lon` | float | `52.52`, `13.40` | Center coordinates for spatial search |
| `radius` | string | `1000m` | Radius (e.g. "1000m", "5km") for spatial search |
| `min_lat`, `max_lat`, `min_lon`, `max_lon` | float | `52.4`, `52.6`, `13.3`, `13.5` | Bounding box coordinates for spatial search |
| `limit` | int | `100` | Maximum number of results to return (default: 100, max: 1000) |
| `from` | int | `0` | Offset for pagination |
| `langs` | string | `en,de` | Comma-separated list of preferred languages |
| `fuzzy` | bool | `true` | Toggle fuzzy matching (true/1) |
| `prefix` | bool | `true` | Toggle prefix matching (true/1) |
| `exact_match` | bool | `true` | PMTiles only: enable slower, more precise non-point geometry filtering per request |
| `key`, `value` | string | `amenity`, `restaurant` | Filter by primary classification |
| `keys`, `values` | string | `amenity,shop` | Comma-separated multi-value filters |

`q` also supports `X near Y`, `X in Y`, `X around Y`, and `X close to Y` patterns in all three search modes.

### Supported Tags

| Category | Searchable Keys | Returned Fields |
| :--- | :--- | :--- |
| Names | `name`, `alt_name`, `short_name`, `old_name`, `brand`, `operator`, `name:lang` | Full name set + enhanced name |
| Classification | `amenity`, `shop`, `tourism`, `leisure`, `place`, `highway`, etc. | `key`, `value`, `keys`, `values` |
| Address | `addr:street`, `addr:housenumber`, `addr:city`, `addr:postcode`, `addr:country`, `addr:floor`, `addr:unit`, `level` | Full address set |
| Metadata | `phone`, `opening_hours`, `wheelchair`, `wikidata`, `wikipedia` | `phone`, `opening_hours`, `wheelchair`, `importance` |

## Benchmark Results (liechtenstein-latest.osm.pbf, 3.19 MB)
```plain
============================================================
INDEX SIZE COMPARISON
============================================================
Scenario             Index Size      Build Time     
------------------------------------------------------------
PBF Scan             0 B (Live)      0s             
PMTiles Scan         0 B (Live)      0s             
Minimal Mode         1.15 MB         396.49ms       
No Geo               19.45 MB        2.83s          
Nodes Only           33.92 MB        3.76s          
Representative Pts   101.73 MB       11.66s         
Centroids (Simple)   101.81 MB       11.20s         
Wiki Redirects       101.81 MB       10.97s         
Addresses            104.28 MB       12.46s         
Bounding Boxes       122.34 MB       15.01s         

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Nodes Only           Combined (Fuzzy+Key)      492.21µs        1         
Nodes Only           Tourism Search            631.87µs        7         
Bounding Boxes       Combined (Fuzzy+Key)      635.99µs        1         
Addresses            Combined (Fuzzy+Key)      646.40µs        1         
Centroids (Simple)   Combined (Fuzzy+Key)      648.69µs        1         
Nodes Only           Shop Search               659.73µs        10        
No Geo               Combined (Fuzzy+Key)      716.38µs        1         
Addresses            Shop Search               731.13µs        10        
Wiki Redirects       Combined (Fuzzy+Key)      751.65µs        1         
No Geo               Shop Search               807.11µs        10        
Representative Pts   Combined (Fuzzy+Key)      852.53µs        1         
Wiki Redirects       Shop Search               872.18µs        10        
Bounding Boxes       Shop Search               879.06µs        10        
Centroids (Simple)   Shop Search               888.86µs        10        
Representative Pts   Shop Search               999.79µs        10        
Centroids (Simple)   Tourism Search            1.30ms          12        
Minimal Mode         Prefix Search             1.36ms          129       
Addresses            Tourism Search            1.38ms          12        
No Geo               Tourism Search            1.42ms          12        
Minimal Mode         Fuzzy Search              1.43ms          127       
Wiki Redirects       Tourism Search            1.52ms          12        
Bounding Boxes       Tourism Search            1.53ms          12        
Representative Pts   Tourism Search            1.54ms          12        
Minimal Mode         Basic Search              1.58ms          127       
Addresses            Address Match             2.00ms          26        
Nodes Only           Prefix Search             2.91ms          149       
Nodes Only           Fuzzy Search              2.94ms          147       
No Geo               Prefix Search             4.51ms          211       
Nodes Only           Value Filter              4.55ms          228       
Nodes Only           Basic Search              4.56ms          228       
No Geo               Fuzzy Search              4.58ms          208       
Nodes Only           Key Filter                4.67ms          228       
Addresses            Prefix Search             4.88ms          211       
Wiki Redirects       Fuzzy Search              4.93ms          208       
Wiki Redirects       Prefix Search             4.97ms          211       
Representative Pts   Fuzzy Search              5.03ms          208       
Centroids (Simple)   Fuzzy Search              5.20ms          208       
Addresses            Fuzzy Search              5.22ms          208       
Centroids (Simple)   Prefix Search             5.52ms          211       
Bounding Boxes       Prefix Search             5.54ms          211       
Representative Pts   Prefix Search             5.70ms          211       
Bounding Boxes       Fuzzy Search              6.07ms          208       
No Geo               Basic Search              7.41ms          1798      
No Geo               Value Filter              7.84ms          1798      
Wiki Redirects       Basic Search              7.85ms          1798      
Centroids (Simple)   Key Filter                8.01ms          1798      
Centroids (Simple)   Value Filter              8.03ms          1798      
Addresses            Basic Search              8.10ms          1798      
Representative Pts   Basic Search              8.29ms          1798      
Addresses            Key Filter                8.35ms          1798      
Wiki Redirects       Key Filter                8.37ms          1798      
Addresses            Value Filter              8.42ms          1798      
Wiki Redirects       Value Filter              8.47ms          1798      
No Geo               Key Filter                8.72ms          1798      
Centroids (Simple)   Basic Search              8.84ms          1798      
Bounding Boxes       Value Filter              9.18ms          1798      
Representative Pts   Key Filter                9.35ms          1798      
Bounding Boxes       Basic Search              9.57ms          1798      
Representative Pts   Value Filter              9.78ms          1798      
Bounding Boxes       Key Filter                9.81ms          1798      
Nodes Only           BBox Search               10.14ms         981       
Nodes Only           Radius Search             10.25ms         1643      
PMTiles Scan         Radius Search             10.82ms         261       
PMTiles Scan         BBox Search               11.41ms         276       
Addresses            BBox Search               17.69ms         2350      
Centroids (Simple)   BBox Search               19.12ms         2350      
Wiki Redirects       BBox Search               19.86ms         2350      
Representative Pts   BBox Search               19.93ms         2349      
Wiki Redirects       Radius Search             20.95ms         4433      
Addresses            Radius Search             21.08ms         4433      
PMTiles Scan         Basic Search              21.47ms         59        
Representative Pts   Radius Search             21.97ms         4431      
Centroids (Simple)   Radius Search             21.99ms         4433      
PMTiles Scan         Shop Search               24.56ms         2         
PMTiles Scan         Value Filter              25.20ms         1         
PMTiles Scan         Combined (Fuzzy+Key)      25.25ms         1         
PMTiles Scan         Key Filter                29.58ms         1         
PMTiles Scan         Fuzzy Search              32.42ms         59        
PMTiles Scan         Tourism Search            32.95ms         5         
PMTiles Scan         Prefix Search             35.12ms         59        
Bounding Boxes       Radius Search             71.91ms         1662      
Bounding Boxes       BBox Search               72.35ms         996       
PBF Scan             Tourism Search            430.68ms        12        
PBF Scan             Shop Search               438.68ms        10        
PBF Scan             Key Filter                440.41ms        2         
PBF Scan             Basic Search              471.36ms        1818      
PBF Scan             Combined (Fuzzy+Key)      473.53ms        2         
PBF Scan             Value Filter              475.48ms        1         
PBF Scan             Fuzzy Search              539.89ms        1818      
PBF Scan             Prefix Search             577.66ms        1870
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
Minimal Mode         103.97 MB       76.76s         
No Geo               2.72 GB         1340.19s       
Nodes Only           8.12 GB         2156.61s       
Representative Pts   10.27 GB        2666.04s       
Centroids (Simple)   10.35 GB        2894.50s       
Wiki Redirects       10.77 GB        2552.05s       
Bounding Boxes       12.33 GB        3137.62s       
Addresses            12.71 GB        2900.85s       

============================================================
FULL PERFORMANCE REPORT (Sorted by Latency)
============================================================
Spatial Mode         Scenario                  Avg Latency     Results   
--------------------------------------------------------------------------------
Minimal Mode         Basic Search              6.52ms          3258      
No Geo               Value Filter              8.68ms          7049      
No Geo               Key Filter                8.74ms          7049      
Nodes Only           Value Filter              8.88ms          3258      
No Geo               Basic Search              8.95ms          7049      
Representative Pts   Value Filter              9.71ms          7049      
Addresses            Value Filter              9.72ms          7049      
Addresses            Key Filter                9.93ms          7049      
Representative Pts   Key Filter                10.10ms         7049      
Centroids (Simple)   Value Filter              10.85ms         7049      
Centroids (Simple)   Key Filter                11.76ms         7049      
Nodes Only           Combined (Fuzzy+Key)      13.29ms         824       
Nodes Only           Key Filter                13.36ms         3258      
Minimal Mode         Fuzzy Search              13.89ms         16203     
Minimal Mode         Prefix Search             14.06ms         16368     
Nodes Only           Basic Search              15.62ms         3258      
Addresses            Combined (Fuzzy+Key)      17.38ms         830       
No Geo               Combined (Fuzzy+Key)      17.59ms         830       
Representative Pts   Combined (Fuzzy+Key)      18.10ms         830       
Bounding Boxes       Value Filter              18.34ms         7049      
Centroids (Simple)   Combined (Fuzzy+Key)      19.25ms         830       
No Geo               Fuzzy Search              20.75ms         31413     
Nodes Only           Fuzzy Search              21.58ms         16402     
No Geo               Prefix Search             22.49ms         31815     
Nodes Only           Prefix Search             23.05ms         16567     
Bounding Boxes       Key Filter                23.09ms         7049      
PMTiles Scan         BBox Search               23.24ms         100       
Bounding Boxes       Combined (Fuzzy+Key)      25.61ms         830       
Representative Pts   Prefix Search             26.39ms         31815     
Centroids (Simple)   Prefix Search             29.51ms         31815     
Addresses            Basic Search              30.54ms         7049      
Addresses            Prefix Search             30.86ms         31815     
Representative Pts   Fuzzy Search              37.61ms         31413     
Bounding Boxes       Basic Search              39.80ms         7049      
Bounding Boxes       Prefix Search             40.21ms         31815     
Addresses            Fuzzy Search              40.72ms         31413     
PMTiles Scan         Radius Search             40.96ms         100       
Bounding Boxes       Fuzzy Search              41.06ms         31413     
Representative Pts   Basic Search              41.22ms         7049      
Centroids (Simple)   Fuzzy Search              42.38ms         31413     
Centroids (Simple)   Basic Search              50.99ms         7049      
Wiki Redirects       Combined (Fuzzy+Key)      56.88ms         830       
Wiki Redirects       Key Filter                58.19ms         7049      
Wiki Redirects       Basic Search              62.42ms         7049      
Wiki Redirects       Value Filter              63.08ms         7049      
Wiki Redirects       Fuzzy Search              72.68ms         31413     
Wiki Redirects       Prefix Search             94.43ms         31815     
PMTiles Scan         Prefix Search             179.82ms        100       
PMTiles Scan         Tourism Search            206.41ms        1         
PMTiles Scan         Basic Search              212.03ms        100       
PMTiles Scan         Key Filter                212.91ms        4         
PMTiles Scan         Fuzzy Search              214.78ms        100       
PMTiles Scan         Value Filter              218.00ms        4         
PMTiles Scan         Shop Search               220.37ms        30        
PMTiles Scan         Combined (Fuzzy+Key)      228.39ms        4         
Nodes Only           Shop Search               261.37ms        1361      
Nodes Only           Tourism Search            264.64ms        217       
Addresses            Tourism Search            280.26ms        689       
Wiki Redirects       Tourism Search            282.78ms        689       
Addresses            Shop Search               283.99ms        1424      
Bounding Boxes       Tourism Search            286.12ms        689       
Bounding Boxes       Shop Search               291.86ms        1424      
No Geo               Shop Search               295.20ms        1424      
No Geo               Tourism Search            296.64ms        689       
Wiki Redirects       Shop Search               298.57ms        1424      
Representative Pts   Shop Search               299.61ms        1424      
Representative Pts   Tourism Search            299.94ms        689       
PBF Scan             Prefix Search             309.02ms        100       
Centroids (Simple)   Shop Search               314.79ms        1424      
Centroids (Simple)   Tourism Search            316.79ms        689       
Nodes Only           BBox Search               365.57ms        24108     
Wiki Redirects       BBox Search               425.61ms        26275     
Addresses            BBox Search               435.07ms        26275     
Representative Pts   BBox Search               443.04ms        26272     
Centroids (Simple)   BBox Search               461.17ms        26275     
Nodes Only           Radius Search             579.26ms        50722     
Wiki Redirects       Radius Search             683.59ms        55557     
PBF Scan             Fuzzy Search              685.55ms        100       
Addresses            Radius Search             701.89ms        55557     
Representative Pts   Radius Search             748.91ms        55554     
PBF Scan             Basic Search              771.35ms        100       
Centroids (Simple)   Radius Search             776.27ms        55557     
PBF Scan             Shop Search               1.50s           100       
Addresses            Address Match             3.05s           5464929   
PBF Scan             Tourism Search            5.63s           100       
Bounding Boxes       Radius Search             21.08s          50686     
Bounding Boxes       BBox Search               21.51s          24108     
PBF Scan             Key Filter                57.47s          11        
PBF Scan             Combined (Fuzzy+Key)      58.22s          11        
PBF Scan             Value Filter              58.69s          2
```


## Install

```sh
go install github.com/chapmanjacobd/poisearch/cmd/poisearch@latest
```

### Prerequisites

- Go 1.22+
- libgeos (GEOS)
- `osmium` (for pre-processing PBFs)

#### Installing GEOS

```sh
# macOS
brew install geos

# Fedora
sudo dnf install geos-devel

# Ubuntu / Debian
sudo apt install libgeos-dev
```

## Features

- Multi-Interface Search: Search via high-performance Bleve index, direct PBF scan    , or PMTiles.
- Rich Metadata: Support for `phone`, `opening_hours`, `wheelchair` accessibility, and detailed classification.
- Enhanced Results: Names are automatically enhanced with `brand`, `operator`, `religion`, or `denomination` (e.g., "St. Mary's (Catholic)").
- Advanced Address Search: Support for house numbers, streets, cities, postcodes, and even `floor`, `unit`, and `level`.
- Spatial Filters: Radius search, bounding box filters, and precise intersection checks.
- On-the-fly Classification: Sophisticated POI classification based on an extensible ontology.

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

## Features

- Multi-Interface Search: Search via high-performance Bleve index, direct PBF scan    , or PMTiles.
- Rich Metadata: Support for `phone`, `opening_hours`, `wheelchair` accessibility, and detailed classification.
- Enhanced Results: Names are automatically enhanced with `brand`, `operator`, `religion`, or `denomination` (e.g., "St. Mary's (Catholic)").
- Advanced Address Search: Support for house numbers, streets, cities, postcodes, and even `floor`, `unit`, and `level`.
- Spatial Filters: Radius search, bounding box filters, and precise intersection checks.
- On-the-fly Classification: Sophisticated POI classification based on an extensible ontology.

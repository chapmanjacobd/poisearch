package search

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	lru "github.com/hashicorp/golang-lru/v2"
)

// CacheStats holds statistics for the query cache.
type CacheStats struct {
	Hits      int64   `json:"hits"`
	Misses    int64   `json:"misses"`
	HitRate   float64 `json:"hit_rate"`
	Size      int     `json:"size"`
	Evictions int64   `json:"evictions"`
}

// QueryCache is a thread-safe LRU cache for search results with TTL.
type QueryCache struct {
	cache *lru.Cache[string, *cacheEntry]
	ttl   time.Duration
	mu    sync.Mutex
	stats CacheStats
}

type cacheEntry struct {
	result    *SerializedResult
	expiresAt time.Time
}

// SerializedResult holds a serialized search result for caching.
type SerializedResult struct {
	Total int64           `json:"total"`
	From  int             `json:"from"`
	Limit int             `json:"limit"`
	Hits  []SerializedHit `json:"hits"`
}

// SerializedHit represents a single search hit for caching.
type SerializedHit struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
	Name  string  `json:"name,omitempty"`
	Key   string  `json:"key,omitempty"`
	Value string  `json:"value,omitempty"`
	Lat   float64 `json:"lat,omitempty"`
	Lon   float64 `json:"lon,omitempty"`
}

// NewQueryCache creates a new query cache with the given capacity and TTL.
func NewQueryCache(capacity int, ttl time.Duration) (*QueryCache, error) {
	cache, err := lru.New[string, *cacheEntry](capacity)
	if err != nil {
		return nil, fmt.Errorf("failed to create LRU cache: %w", err)
	}

	return &QueryCache{
		cache: cache,
		ttl:   ttl,
	}, nil
}

// Get retrieves a cached result if it exists and hasn't expired.
func (c *QueryCache) Get(key string) (*SerializedResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.cache.Get(key)
	if !ok {
		c.stats.Misses++
		return nil, false
	}

	if time.Now().After(entry.expiresAt) {
		// Entry has expired, remove it
		c.cache.Remove(key)
		c.stats.Misses++
		return nil, false
	}

	c.stats.Hits++
	return entry.result, true
}

// Set stores a result in the cache with the configured TTL.
func (c *QueryCache) Set(key string, result *SerializedResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache.Add(key, &cacheEntry{
		result:    result,
		expiresAt: time.Now().Add(c.ttl),
	})
}

// Clear removes all entries from the cache and resets stats.
func (c *QueryCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache.Purge()
	c.stats = CacheStats{}
}

// Stats returns current cache statistics.
func (c *QueryCache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	stats := c.stats
	stats.Size = c.cache.Len()

	// Calculate hit rate
	total := stats.Hits + stats.Misses
	if total > 0 {
		stats.HitRate = float64(stats.Hits) / float64(total)
	}

	return stats
}

// BuildCacheKey creates a cache key from search parameters.
// For geo queries, only non-geo parameters are used to increase cache hits.
func BuildCacheKey(params SearchParams) string {
	h := sha256.New()

	// Always include these base parameters
	fmt.Fprintf(
		h,
		"q=%s&limit=%d&from=%d&fuzzy=%t&prefix=%t&key=%s&value=%s&keys=%v&values=%v&street=%s&housenumber=%s&postcode=%s&city=%s&country=%s",
		params.Query,
		params.Limit,
		params.From,
		params.Fuzzy,
		params.Prefix,
		params.Key,
		params.Value,
		params.Keys,
		params.Values,
		params.Street,
		params.HouseNumber,
		params.Postcode,
		params.City,
		params.Country,
	)

	// Only include geo params for non-geo queries
	// A query is considered "geo" if it has lat/lon/radius or bbox parameters
	isGeo := (params.Lat != nil && params.Lon != nil && params.Radius != "") ||
		(params.MinLat != nil && params.MaxLat != nil && params.MinLon != nil && params.MaxLon != nil)

	if !isGeo {
		fmt.Fprintf(h, "&langs=%v", params.Langs)
	}

	hash := hex.EncodeToString(h.Sum(nil))
	return hash
}

// IsGeoQuery checks if a search involves geo-spatial parameters.
func IsGeoQuery(params SearchParams) bool {
	return (params.Lat != nil && params.Lon != nil && params.Radius != "") ||
		(params.MinLat != nil && params.MaxLat != nil && params.MinLon != nil && params.MaxLon != nil)
}

// SerializeResult converts a bleve.SearchResult to a cacheable format.
func SerializeResult(result *bleve.SearchResult) *SerializedResult {
	if result == nil {
		return nil
	}

	hits := make([]SerializedHit, 0, len(result.Hits))
	for _, hit := range result.Hits {
		sHit := SerializedHit{
			ID:    hit.ID,
			Score: hit.Score,
		}

		if name, ok := hit.Fields["name"].(string); ok {
			sHit.Name = name
		}
		if key, ok := hit.Fields["key"].(string); ok {
			sHit.Key = key
		}
		if value, ok := hit.Fields["value"].(string); ok {
			sHit.Value = value
		}

		// Extract geometry
		if geom, ok := hit.Fields["geometry"].(map[string]any); ok {
			if lat, ok := geom["lat"].(float64); ok {
				sHit.Lat = lat
			}
			if lon, ok := geom["lon"].(float64); ok {
				sHit.Lon = lon
			}
		}

		hits = append(hits, sHit)
	}

	sr := &SerializedResult{
		Total: int64(result.Total),
		Hits:  hits,
	}
	if result.Request != nil {
		sr.From = result.Request.From
		sr.Limit = result.Request.Size
	}
	return sr
}

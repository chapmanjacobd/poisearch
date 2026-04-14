package search

import (
	"testing"
	"time"
)

func TestQueryCache_BasicOperations(t *testing.T) {
	cache, err := NewQueryCache(100, time.Minute)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	// Test set and get
	result := &SerializedResult{
		Total: 10,
		From:  0,
		Limit: 10,
		Hits: []SerializedHit{
			{ID: "node/1", Score: 1.5, Name: "Test Place"},
		},
	}

	cache.Set("test-key", result)

	got, found := cache.Get("test-key")
	if !found {
		t.Fatal("expected to find cached result")
	}
	if got.Total != 10 {
		t.Errorf("expected total=10, got %d", got.Total)
	}
	if len(got.Hits) != 1 {
		t.Errorf("expected 1 hit, got %d", len(got.Hits))
	}
	if got.Hits[0].Name != "Test Place" {
		t.Errorf("expected name='Test Place', got %s", got.Hits[0].Name)
	}
}

func TestQueryCache_TTL(t *testing.T) {
	// Create cache with very short TTL
	cache, err := NewQueryCache(100, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	result := &SerializedResult{Total: 5}
	cache.Set("ttl-key", result)

	// Should exist immediately
	_, found := cache.Get("ttl-key")
	if !found {
		t.Fatal("expected cache hit immediately after set")
	}

	// Wait for TTL to expire
	time.Sleep(100 * time.Millisecond)

	// Should be expired now
	_, found = cache.Get("ttl-key")
	if found {
		t.Error("expected cache miss after TTL expiration")
	}
}

func TestQueryCache_LRU_Eviction(t *testing.T) {
	// Create cache with capacity 2
	cache, err := NewQueryCache(2, time.Minute)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	// Fill cache
	cache.Set("key1", &SerializedResult{Total: 1})
	cache.Set("key2", &SerializedResult{Total: 2})

	// Add third item - should evict key1 (least recently used)
	cache.Set("key3", &SerializedResult{Total: 3})

	// key1 should be evicted
	_, found := cache.Get("key1")
	if found {
		t.Error("expected key1 to be evicted")
	}

	// key2 and key3 should still exist
	_, found = cache.Get("key2")
	if !found {
		t.Error("expected key2 to still exist")
	}
	_, found = cache.Get("key3")
	if !found {
		t.Error("expected key3 to still exist")
	}
}

func TestQueryCache_Clear(t *testing.T) {
	cache, err := NewQueryCache(100, time.Minute)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	cache.Set("key1", &SerializedResult{Total: 1})
	cache.Set("key2", &SerializedResult{Total: 2})

	cache.Clear()

	_, found := cache.Get("key1")
	if found {
		t.Error("expected key1 to be cleared")
	}
	_, found = cache.Get("key2")
	if found {
		t.Error("expected key2 to be cleared")
	}
}

func TestQueryCache_Stats(t *testing.T) {
	cache, err := NewQueryCache(100, time.Minute)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	// Generate some hits and misses
	cache.Set("key1", &SerializedResult{Total: 1})
	cache.Get("key1")       // Hit
	cache.Get("key1")       // Hit
	cache.Get("nonexistent") // Miss

	stats := cache.Stats()
	if stats.Hits != 2 {
		t.Errorf("expected 2 hits, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", stats.Misses)
	}
	if stats.HitRate != 2.0/3.0 {
		t.Errorf("expected hit rate 0.667, got %f", stats.HitRate)
	}
	if stats.Size != 1 {
		t.Errorf("expected size 1, got %d", stats.Size)
	}
}

func TestBuildCacheKey_GeoVsNonGeo(t *testing.T) {
	// Non-geo query
	params1 := SearchParams{
		Query:  "restaurant",
		Limit:  10,
		Langs:  []string{"en"},
		Fuzzy:  true,
	}
	key1 := BuildCacheKey(params1)

	// Same non-geo query should have same key
	params2 := SearchParams{
		Query:  "restaurant",
		Limit:  10,
		Langs:  []string{"en"},
		Fuzzy:  true,
	}
	key2 := BuildCacheKey(params2)

	if key1 != key2 {
		t.Error("expected same cache key for identical non-geo queries")
	}

	// Geo query should ignore geo params in cache key
	lat1 := 52.5
	lon1 := 13.4
	params3 := SearchParams{
		Query:  "restaurant",
		Lat:    &lat1,
		Lon:    &lon1,
		Radius: "1000m",
		Limit:  10,
	}
	key3 := BuildCacheKey(params3)

	lat2 := 48.8
	lon2 := 2.3
	params4 := SearchParams{
		Query:  "restaurant",
		Lat:    &lat2,
		Lon:    &lon2,
		Radius: "5000m",
		Limit:  10,
	}
	key4 := BuildCacheKey(params4)

	// Both geo queries for "restaurant" should have same key
	if key3 != key4 {
		t.Error("expected same cache key for geo queries (geo params ignored)")
	}

	// But non-geo and geo should differ (langs included in non-geo)
	if key1 == key3 {
		t.Error("expected different keys for non-geo vs geo queries")
	}
}

func TestIsGeoQuery(t *testing.T) {
	tests := []struct {
		name   string
		params SearchParams
		want   bool
	}{
		{
			name:   "empty query",
			params: SearchParams{},
			want:   false,
		},
		{
			name: "radius search",
			params: SearchParams{
				Lat:    ptrFloat64(52.5),
				Lon:    ptrFloat64(13.4),
				Radius: "1000m",
			},
			want: true,
		},
		{
			name: "bbox search",
			params: SearchParams{
				MinLat: ptrFloat64(52.0),
				MaxLat: ptrFloat64(53.0),
				MinLon: ptrFloat64(13.0),
				MaxLon: ptrFloat64(14.0),
			},
			want: true,
		},
		{
			name: "text only with lat/lon but no radius",
			params: SearchParams{
				Query: "restaurant",
				Lat:   ptrFloat64(52.5),
				Lon:   ptrFloat64(13.4),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsGeoQuery(tt.params)
			if got != tt.want {
				t.Errorf("IsGeoQuery() = %v, want %v", got, tt.want)
			}
		})
	}
}

func ptrFloat64(v float64) *float64 {
	return &v
}

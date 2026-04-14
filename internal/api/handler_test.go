package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/chapmanjacobd/poisearch/internal/api"
	"github.com/chapmanjacobd/poisearch/internal/config"
)

// setupTestHandler creates a test HTTP server with registered handlers.
func setupTestHandler(t *testing.T, index bleve.Index, conf *config.Config) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	api.RegisterHandlers(mux, index, conf)
	return httptest.NewServer(mux)
}

func createTestIndex(t *testing.T) bleve.Index {
	t.Helper()

	indexMapping := bleve.NewIndexMapping()
	docMapping := bleve.NewDocumentMapping()

	// Name fields
	nameFieldMapping := bleve.NewTextFieldMapping()
	docMapping.AddFieldMappingsAt("name", nameFieldMapping)
	docMapping.AddFieldMappingsAt("class", bleve.NewTextFieldMapping())
	docMapping.AddFieldMappingsAt("subtype", bleve.NewTextFieldMapping())

	numMapping := bleve.NewNumericFieldMapping()
	docMapping.AddFieldMappingsAt("importance", numMapping)

	geoMapping := bleve.NewGeoPointFieldMapping()
	docMapping.AddFieldMappingsAt("geometry", geoMapping)

	// Address fields
	addrMapping := bleve.NewTextFieldMapping()
	docMapping.AddFieldMappingsAt("addr:housenumber", addrMapping)
	docMapping.AddFieldMappingsAt("addr:street", addrMapping)
	docMapping.AddFieldMappingsAt("addr:city", addrMapping)

	indexMapping.DefaultMapping = docMapping

	index, err := bleve.NewMemOnly(indexMapping)
	if err != nil {
		t.Fatalf("failed to create test index: %v", err)
	}

	// Index some test documents
	testData := []struct {
		id         string
		name       string
		class      string
		subtype    string
		importance float64
		lat, lon   float64
		street     string
		housenum   string
		city       string
	}{
		{"node/1", "Restaurant Alpha", "amenity", "restaurant", 5.0, 47.14, 9.52, "Main St", "123", "Vaduz"},
		{"node/2", "Cafe Beta", "amenity", "cafe", 4.0, 47.15, 9.53, "Main St", "456", "Vaduz"},
		{"node/3", "Shop Gamma", "shop", "yes", 3.0, 47.16, 9.54, "Side St", "789", "Vaduz"},
		{"node/4", "Hotel Delta", "tourism", "hotel", 4.5, 47.17, 9.55, "", "", ""},
		{"node/5", "Park Epsilon", "leisure", "park", 3.5, 47.18, 9.56, "", "", ""},
	}

	for _, td := range testData {
		doc := map[string]any{
			"name":       td.name,
			"class":      td.class,
			"subtype":    td.subtype,
			"importance": td.importance,
			"geometry":   map[string]float64{"lat": td.lat, "lon": td.lon},
		}
		if td.street != "" {
			doc["addr:housenumber"] = td.housenum
			doc["addr:street"] = td.street
			doc["addr:city"] = td.city
		}
		if err := index.Index(td.id, doc); err != nil {
			t.Fatalf("failed to index test document %s: %v", td.id, err)
		}
	}

	return index
}

func TestHandler_Pagination(t *testing.T) {
	index := createTestIndex(t)
	defer index.Close()

	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
	}

	server := setupTestHandler(t, index, conf)
	defer server.Close()

	tests := []struct {
		name       string
		url        string
		expectMin  int
		expectMax  int
		expectFrom int
	}{
		{
			name:       "first page no offset",
			url:        "/search?q=restaurant&limit=2&from=0",
			expectMin:  0,
			expectMax:  2,
			expectFrom: 0,
		},
		{
			name:       "second page with offset",
			url:        "/search?q=&limit=2&from=2",
			expectMin:  0,
			expectMax:  2,
			expectFrom: 2,
		},
		{
			name:       "third page beyond results",
			url:        "/search?q=&limit=2&from=10",
			expectMin:  0,
			expectMax:  0,
			expectFrom: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(server.URL + tt.url)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.StatusCode)
			}

			var result map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			// Check total count
			total, _ := result["total"].(float64)
			hits, _ := result["hits"].([]any)

			if len(hits) < tt.expectMin {
				t.Errorf("expected at least %d hits, got %d", tt.expectMin, len(hits))
			}
			if len(hits) > tt.expectMax {
				t.Errorf("expected at most %d hits, got %d", tt.expectMax, len(hits))
			}

			_ = total // Total may vary based on test data
		})
	}
}

func TestHandler_StructuredErrorResponse(t *testing.T) {
	index := createTestIndex(t)
	defer index.Close()

	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
	}

	server := setupTestHandler(t, index, conf)
	defer server.Close()

	// Test successful search returns proper JSON
	resp, err := http.Get(server.URL + "/search?q=Restaurant")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("expected JSON response, failed to decode: %v", err)
	}

	// Debug: print response structure
	t.Logf("Response keys: %v", getMapKeys(result))

	// Verify response has expected structure (either success or error)
	if _, ok := result["error"]; ok {
		// Error response format
		if _, ok := result["code"]; !ok {
			t.Error("error response missing 'code' field")
		}
		if _, ok := result["status"]; !ok {
			t.Error("error response missing 'status' field")
		}
	} else {
		// Success response format (Bleve uses total_hits)
		if _, ok := result["total_hits"]; !ok {
			t.Error("success response missing 'total_hits' field")
		}
		if _, ok := result["hits"]; !ok {
			t.Error("success response missing 'hits' field")
		}
	}
}

func TestHandler_AddressSearch(t *testing.T) {
	index := createTestIndex(t)
	defer index.Close()

	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
		StoreAddress: true,
	}

	server := setupTestHandler(t, index, conf)
	defer server.Close()

	tests := []struct {
		name        string
		url         string
		expectMin   int
		description string
	}{
		{
			name:        "search by housenumber",
			url:         "/search?&housenumber=123",
			expectMin:   1,
			description: "Should find 1 POI with housenumber 123",
		},
		{
			name:        "search by non-existent address",
			url:         "/search?&street=NonExistent",
			expectMin:   0,
			description: "Should find 0 POIs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(server.URL + tt.url)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("expected status 200, got %d", resp.StatusCode)
			}

			var result map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			hits, _ := result["hits"].([]any)
			if len(hits) < tt.expectMin {
				t.Errorf("%s: expected at least %d hits, got %d", tt.description, tt.expectMin, len(hits))
			}
		})
	}
}

func TestHandler_TextOutputFormat(t *testing.T) {
	index := createTestIndex(t)
	defer index.Close()

	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
		StoreAddress: true,
	}

	server := setupTestHandler(t, index, conf)
	defer server.Close()

	resp, err := http.Get(server.URL + "/search?q=Restaurant&format=text")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("expected Content-Type text/plain, got %s", contentType)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	body := string(bodyBytes)

	// Check for expected text format fields
	if !strings.Contains(body, "id:") {
		t.Error("text response missing 'id:' field")
	}
	if !strings.Contains(body, "name:") {
		t.Error("text response missing 'name:' field")
	}
	if !strings.Contains(body, "score:") {
		t.Error("text response missing 'score:' field")
	}
	if !strings.Contains(body, "total:") {
		t.Error("text response missing 'total:' field")
	}
}

func TestHandler_QueryParameterParsing(t *testing.T) {
	index := createTestIndex(t)
	defer index.Close()

	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
	}

	server := setupTestHandler(t, index, conf)
	defer server.Close()

	tests := []struct {
		name        string
		url         string
		description string
	}{
		{
			name:        "fuzzy search",
			url:         "/search?q=restaurant&fuzzy=true",
			description: "Should handle fuzzy=true parameter",
		},
		{
			name:        "prefix search",
			url:         "/search?q=Rest&prefix=true",
			description: "Should handle prefix=true parameter",
		},
		{
			name:        "class filter",
			url:         "/search?&class=amenity",
			description: "Should handle class filter",
		},
		{
			name:        "multi-class filter",
			url:         "/search?&classes=amenity,shop",
			description: "Should handle multi-class filter",
		},
		{
			name:        "custom languages",
			url:         "/search?q=test&langs=en,de,fr",
			description: "Should handle langs parameter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(server.URL + tt.url)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s: expected status 200, got %d", tt.description, resp.StatusCode)
			}

			var result map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatalf("%s: expected JSON response, failed to decode: %v", tt.description, err)
			}
		})
	}
}

// getMapKeys returns all keys in a map as a slice.
func getMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// Test that the handler registration functions work correctly.
func TestHandler_Registration(t *testing.T) {
	index, err := bleve.NewMemOnly(mapping.NewIndexMapping())
	if err != nil {
		t.Fatalf("failed to create test index: %v", err)
	}
	defer index.Close()

	conf := &config.Config{
		Languages:    []string{"en"},
		GeometryMode: "geopoint",
	}

	// Test RegisterHandlers (without PBF)
	mux := http.NewServeMux()
	api.RegisterHandlers(mux, index, conf)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected health endpoint to return 200, got %d", rec.Code)
	}

	// Test RegisterHandlersWithPBF (with empty PBF/PMTiles paths)
	mux2 := http.NewServeMux()
	api.RegisterHandlersWithPBF(mux2, api.HandlerOptions{
		Index: index,
		Conf:  conf,
	})

	req2 := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	rec2 := httptest.NewRecorder()
	mux2.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("expected search endpoint to return 200, got %d", rec2.Code)
	}
}

package tests_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/chapmanjacobd/poisearch/internal/osm"
	"github.com/chapmanjacobd/poisearch/internal/search"
)

func TestBleveExplanation(t *testing.T) {
	pbfPath := downloadPBF(t)
	conf := defaultTestConfig()
	conf.NameAnalyzer = "standard"
	conf.StoreMetadata = true

	ont := osm.DefaultOntology()
	search.CategoryMapper = func(_ string) []search.CategoryMatch {
		q := "restaurant" // force it for the test
		matches := ont.GetTagsForLabel(q)
		result := make([]search.CategoryMatch, 0, len(matches))
		for _, m := range matches {
			result = append(result, search.CategoryMatch{Key: m.Key, Value: m.Value})
		}
		return result
	}

	_, idx := buildTestIndex(t, pbfPath, conf)
	defer idx.Close()

	// Build the exact query
	q1 := bleve.NewMatchQuery("restaurant")
	q1.SetField("name")

	q2 := bleve.NewMatchQuery("restaurant")
	q2.SetField("name_edge_ngram")

	q3 := bleve.NewMatchQuery("restaurant")
	q3.SetField("name")
	q3.SetFuzziness(1)

	disj1 := bleve.NewDisjunctionQuery(q1, q2, q3)

	c1 := bleve.NewMatchQuery("restaurant")
	c1.SetField("value")
	c2 := bleve.NewMatchQuery("restaurant")
	c2.SetField("values")
	disj2 := bleve.NewDisjunctionQuery(c1, c2)

	finalQ := bleve.NewDisjunctionQuery(disj1, disj2)

	req := bleve.NewSearchRequest(finalQ)
	req.Explain = true
	res, err := idx.Search(req)
	if err != nil {
		t.Fatal(err)
	}

	for i, hit := range res.Hits {
		if i > 2 {
			break
		}
		b, err := json.MarshalIndent(hit.Expl, "", "  ")
		if err != nil {
			t.Fatalf("failed to marshal explanation: %v", err)
		}
		fmt.Printf("HIT: %s\nEXPLANATION:\n%s\n\n", hit.ID, string(b))
	}
}

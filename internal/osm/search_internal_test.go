package osm

import (
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/search"
)

func TestComputeDirectScore_PlaceIntentExactPlaceOutranksPartialMatch(t *testing.T) {
	params := search.SearchParams{
		Query: "Vaduz",
		Langs: []string{"en"},
	}

	exactScore := computeDirectScore(
		map[string]string{"name": "Vaduz"},
		[]*Classification{{Key: "place", Value: "town"}},
		params,
		"vaduz",
		13,
	)
	partialScore := computeDirectScore(
		map[string]string{"name": "Vaduz Post"},
		[]*Classification{{Key: "amenity", Value: "bus"}},
		params,
		"vaduz",
		19,
	)

	if exactScore <= partialScore {
		t.Fatalf("exact place score = %f, partial score = %f, want exact > partial", exactScore, partialScore)
	}
}

func TestComputeDirectScore_PlaceIntentBoostRequiresPlaceClassification(t *testing.T) {
	params := search.SearchParams{
		Query: "Vaduz",
		Langs: []string{"en"},
	}

	placeScore := computeDirectScore(
		map[string]string{"name": "Vaduz"},
		[]*Classification{{Key: "place", Value: "town"}},
		params,
		"vaduz",
		13,
	)
	poiScore := computeDirectScore(
		map[string]string{"name": "Vaduz"},
		[]*Classification{{Key: "amenity", Value: "information"}},
		params,
		"vaduz",
		13,
	)

	if placeScore <= poiScore {
		t.Fatalf("place score = %f, poi score = %f, want place-specific boost", placeScore, poiScore)
	}
}

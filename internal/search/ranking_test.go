package search_test

import (
	"testing"

	"github.com/chapmanjacobd/poisearch/internal/search"
)

func TestSharedRankingScore_ExactPlaceOutranksExactPrimaryPOI(t *testing.T) {
	params := search.SearchParams{Query: "Vaduz"}

	placeScore := search.SharedRankingScore(params, search.RankingSignals{
		NameMatchScore: 400,
		EntityTier:     search.EntityTierPlace,
		Importance:     13,
		ExactNameMatch: true,
		HasName:        true,
	})
	poiScore := search.SharedRankingScore(params, search.RankingSignals{
		NameMatchScore: 400,
		EntityTier:     search.EntityTierPrimary,
		Importance:     13,
		ExactNameMatch: true,
		HasName:        true,
	})

	if placeScore <= poiScore {
		t.Fatalf("place score = %f, poi score = %f, want place > poi", placeScore, poiScore)
	}
}

func TestSharedRankingScore_StrongCategoryPrimaryOutranksWeakPlace(t *testing.T) {
	params := search.SearchParams{Query: "restaurant"}

	weakPlaceScore := search.SharedRankingScore(params, search.RankingSignals{
		NameMatchScore: 300,
		EntityTier:     search.EntityTierPlace,
		Importance:     15,
		HasName:        true,
	})
	categoryPOIScore := search.SharedRankingScore(params, search.RankingSignals{
		CategoryMatchScore: 320,
		EntityTier:         search.EntityTierPrimary,
		Importance:         5,
		HasName:            true,
	})

	if categoryPOIScore <= weakPlaceScore {
		t.Fatalf(
			"category POI score = %f, weak place score = %f, want category POI > weak place",
			categoryPOIScore,
			weakPlaceScore,
		)
	}
}

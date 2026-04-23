package search

import "strings"

// IsPlaceIntentQuery reports whether the query looks like a raw place-name lookup
// rather than a category, address, or metadata search.
func IsPlaceIntentQuery(params SearchParams) bool {
	if strings.TrimSpace(params.Query) == "" || IsNearQuery(params.Query) {
		return false
	}
	if params.QueryFields() > 3 {
		return false
	}
	if params.Key != "" || params.Value != "" || len(params.Keys) > 0 || len(params.Values) > 0 {
		return false
	}
	if params.Street != "" || params.HouseNumber != "" || params.Postcode != "" ||
		params.City != "" || params.Country != "" || params.Floor != "" ||
		params.Unit != "" || params.Level != "" {

		return false
	}
	if params.Phone != "" || params.Wheelchair != "" || params.OpeningHours != "" {
		return false
	}
	return true
}

// IsPlaceLikeClassification reports whether a result represents a place/settlement-like feature.
func IsPlaceLikeClassification(key, value string) bool {
	if key != "place" {
		return false
	}
	switch value {
	case "city", "town", "village", "hamlet", "suburb", "quarter", "neighbourhood", "locality",
		"municipality", "county", "state", "province", "region", "country", "island", "islet":
		return true
	default:
		return true
	}
}

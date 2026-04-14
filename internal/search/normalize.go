package search

import (
	"strings"
)

// normalizeQuery applies text normalization to improve search matching.
// This provides ICU-like normalization for non-ASCII characters:
// - German umlauts: ä->ae, ö->oe, ü->ue, ß->ss
// - Accented characters: é->e, ñ->n, etc.
// - Common transliterations
//
// This runs at query time and index time, adding negligible overhead
// while significantly improving recall for international text.
func normalizeQuery(q string) string {
	// Lowercase first
	q = strings.ToLower(q)

	// German umlauts (most common in OSM data)
	q = strings.NewReplacer(
		"ä", "ae",
		"ö", "oe",
		"ü", "ue",
		"Ä", "Ae",
		"Ö", "Oe",
		"Ü", "Ue",
		"ß", "ss",
	).Replace(q)

	// Common accented characters
	q = transliterate(q)

	return q
}

// transliterate converts accented/unicode characters to ASCII equivalents.
func transliterate(s string) string {
	// Common replacements for OSM data
	replacer := strings.NewReplacer(
		// Latin-1 supplement
		"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "ae", "å", "a",
		"è", "e", "é", "e", "ê", "e", "ë", "e",
		"ì", "i", "í", "i", "î", "i", "ï", "i",
		"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "oe",
		"ù", "u", "ú", "u", "û", "u", "ü", "ue",
		"ý", "y", "ÿ", "y",
		"ñ", "n", "ç", "c",
		// Common non-Latin (frequent in OSM)
		"ø", "o",
		"æ", "ae",
		"þ", "th", "ð", "d",
	)
	return replacer.Replace(s)
}

package search

import "unicode/utf8"

// Similarity is a portable trigram similarity of the folded a and b: the
// Jaccard index of their padded trigram sets, close to pg_trgm's for
// ordering names. It is 0 when either side is empty.
func Similarity(a, b string) float64 {
	return similarity(trigrams(Fold(a)), trigrams(Fold(b)))
}

func similarity(left, right map[string]struct{}) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	shared := 0
	for gram := range left {
		if _, ok := right[gram]; ok {
			shared++
		}
	}
	return float64(shared) / float64(len(left)+len(right)-shared)
}

// trigrams are the padded trigrams of an already folded value: "  a", " ab",
// "abc", ..., "yz " — the leading pad makes prefixes weigh more.
func trigrams(value string) map[string]struct{} {
	out := map[string]struct{}{}
	if value == "" {
		return out
	}
	runes := []rune("  " + value + " ")
	for i := 0; i+3 <= len(runes); i++ {
		gram := string(runes[i : i+3])
		if utf8.RuneCountInString(gram) == 3 {
			out[gram] = struct{}{}
		}
	}
	return out
}

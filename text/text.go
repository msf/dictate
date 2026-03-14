package text

import (
	"strings"
	"unicode"
)

// NormalizeToken lowercases and strips punctuation, keeping letters, digits, and apostrophes.
func NormalizeToken(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// OverlapCount returns the length of the longest suffix of a that matches
// a prefix of b, using normalized token comparison.
func OverlapCount(a, b []string) int {
	max := min(len(b), len(a))
	for k := max; k >= 1; k-- {
		match := true
		for i := range k {
			if NormalizeToken(a[len(a)-k+i]) != NormalizeToken(b[i]) {
				match = false
				break
			}
		}
		if match {
			return k
		}
	}
	return 0
}

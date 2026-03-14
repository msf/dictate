package main

import (
	"strconv"
	"strings"
	"unicode"

	textutil "dictate/text"
)

func extractText(lines []string) string {
	var merged []string
	for _, line := range lines {
		text := timestampRe.ReplaceAllString(line, "")
		text = strings.TrimSpace(text)
		if text != "" {
			words := strings.Fields(text)
			if len(words) == 0 {
				continue
			}
			if len(merged) == 0 {
				merged = append(merged, words...)
				continue
			}
			overlap := textutil.OverlapCount(merged, words)
			merged = append(merged, words[overlap:]...)
		}
	}
	return strings.Join(merged, " ")
}

func computeWER(reference, hypothesis string) float64 {
	ref := normalizeWords(reference)
	hyp := normalizeWords(hypothesis)

	if len(ref) == 0 {
		if len(hyp) == 0 {
			return 0
		}
		return 1
	}

	n, m := len(ref), len(hyp)
	prev := make([]int, m+1)
	curr := make([]int, m+1)

	for j := 0; j <= m; j++ {
		prev[j] = j
	}

	for i := 1; i <= n; i++ {
		curr[0] = i
		for j := 1; j <= m; j++ {
			cost := 1
			if ref[i-1] == hyp[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev, curr = curr, prev
	}

	return float64(prev[m]) / float64(n)
}

func normalizeWords(s string) []string {
	s = strings.ToLower(s)
	var buf strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' {
			buf.WriteRune(r)
		} else {
			buf.WriteRune(' ')
		}
	}
	return strings.Fields(buf.String())
}

func extractEncodeMS(stderr string) float64 {
	m := encodeTimingRe.FindStringSubmatch(stderr)
	if len(m) != 3 {
		return 0
	}
	ms, err := strconv.ParseFloat(m[2], 64)
	if err != nil {
		return 0
	}
	return ms
}

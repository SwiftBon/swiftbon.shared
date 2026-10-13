// Package names is the one name normaliser and matcher.
//
// Three services compare a person or company name against another source: kyc
// (BVN record against NIN record), payouts (name enquiry against the
// beneficiary) and documents (OCR text against the registry's legal name). If
// each built its own matcher, the same pair of names would pass in one service
// and fail in another. They share this one.
//
// The match threshold is deliberately not in this package. It is a runtime
// setting each caller reads from its own configuration and passes in.
package names

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Kind says what is being compared, because company names carry legal-form
// suffixes that personal names do not.
type Kind int

const (
	Person Kind = iota + 1
	Company
)

// CompanySuffixEquivalent maps a spelling of a legal-form suffix to the form
// both sides are rewritten to before comparing, so "Ltd" and "Limited" are one
// token. Keys and values are in normalised (upper-case) form. A key may be more
// than one word.
//
// GMBH, LLC, ENTERPRISES and FOUNDATION have no alternative spelling to
// reconcile (L.L.C. becomes LLC when punctuation is dropped), so they compare
// as ordinary tokens: "X Ltd" and "X Foundation" are different entities and
// score as such.
var CompanySuffixEquivalent = map[string]string{
	"LIMITED":                "LTD",
	"PUBLIC LIMITED COMPANY": "PLC",
	"INCORPORATED":           "INC",
}

// Normalise puts a name in comparison form: Unicode NFKD, diacritics removed,
// upper-cased, apostrophes and full stops dropped (O'BRIEN becomes OBRIEN,
// L.L.C. becomes LLC), every other non-alphanumeric run treated as a space
// (ANNE-MARIE becomes ANNE MARIE), "&" read as AND, whitespace collapsed.
func Normalise(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFKD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// A diacritic: NFKD split it from its base letter. Drop it.
		case r == '\'' || r == '’' || r == '‘' || r == '`' || r == '.':
			// Joins rather than separates.
		case r == '&':
			b.WriteString(" AND ")
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// canonical returns the normalised form with company suffix spellings unified.
func canonical(s string, kind Kind) string {
	n := Normalise(s)
	if kind != Company {
		return n
	}
	// Longest key first so PUBLIC LIMITED COMPANY is replaced as a phrase before
	// LIMITED is replaced on its own.
	keys := make([]string, 0, len(CompanySuffixEquivalent))
	for k := range CompanySuffixEquivalent {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	padded := " " + n + " "
	for _, k := range keys {
		padded = strings.ReplaceAll(padded, " "+k+" ", " "+CompanySuffixEquivalent[k]+" ")
	}
	return strings.TrimSpace(padded)
}

// Score returns the similarity of a and b in [0, 1], using a token-set ratio:
// word order is ignored, repeated words count once, and the words the two names
// share are compared apart from the ones they do not.
//
// One departure from the textbook algorithm: when one name's words are a strict
// subset of the other's, that does not score 1. The textbook version would rate
// ADAEZE OKONKWO a perfect match for ADAEZE OKONKWO NWOSU, which for a person
// is a different person. The two names are compared whole instead.
//
// An empty name scores 0, including against another empty name.
func Score(a, b string, kind Kind) float64 {
	ta, tb := tokens(canonical(a, kind)), tokens(canonical(b, kind))
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inA := set(ta)
	inB := set(tb)
	var common, onlyA, onlyB []string
	for _, t := range ta {
		if inB[t] {
			common = append(common, t)
		} else {
			onlyA = append(onlyA, t)
		}
	}
	for _, t := range tb {
		if !inA[t] {
			onlyB = append(onlyB, t)
		}
	}
	sort.Strings(common)
	sort.Strings(onlyA)
	sort.Strings(onlyB)

	t0 := strings.Join(common, " ")
	t1 := strings.TrimSpace(t0 + " " + strings.Join(onlyA, " "))
	t2 := strings.TrimSpace(t0 + " " + strings.Join(onlyB, " "))

	best := ratio(t1, t2)
	if len(onlyA) > 0 && len(onlyB) > 0 && t0 != "" {
		best = math.Max(best, math.Max(ratio(t0, t1), ratio(t0, t2)))
	}
	return best
}

// Match scores a against b and reports whether the score reaches threshold.
// The threshold comes from the caller's configuration (for example
// kyc.related_party_name_match_threshold); a threshold outside (0, 1] or NaN
// never matches, so a misconfigured value fails closed.
func Match(a, b string, kind Kind, threshold float64) (score float64, matched bool) {
	score = Score(a, b, kind)
	if math.IsNaN(threshold) || threshold <= 0 || threshold > 1 {
		return score, false
	}
	return score, score >= threshold
}

func tokens(s string) []string {
	if s == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.Fields(s) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func set(ts []string) map[string]bool {
	m := make(map[string]bool, len(ts))
	for _, t := range ts {
		m[t] = true
	}
	return m
}

// ratio is 1 - editDistance/longerLength over runes.
func ratio(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	longer := max(len(ra), len(rb))
	if longer == 0 {
		return 1
	}
	return 1 - float64(levenshtein(ra, rb))/float64(longer)
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

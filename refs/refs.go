// Package refs formats and parses the human references customers and staff
// quote to each other: PO-20896, BEN-3391, CASE-4182.
//
// The number comes from the owning service's Postgres sequence. This package
// only formats and parses; it never allocates a number.
package refs

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Prefix identifies what a reference points at.
type Prefix string

const (
	Payout      Prefix = "PO"
	Beneficiary Prefix = "BEN"
	Support     Prefix = "SUP"
	Decision    Prefix = "DEC"
	Case        Prefix = "CASE"
	RFI         Prefix = "RFI"
	Approval    Prefix = "APR"
)

// widths is the minimum number of digits for each prefix, taken from the
// mockup (PO-20896, BEN-3391, SUP-2291, DEC-1184, CASE-4182). A number with
// fewer digits is zero-padded; one with more is never truncated.
var widths = map[Prefix]int{
	Payout:      5,
	Beneficiary: 4,
	Support:     4,
	Decision:    4,
	Case:        4,
	RFI:         4,
	Approval:    4,
}

// ErrInvalid is matched by every Parse failure.
var ErrInvalid = errors.New("refs: invalid reference")

// Prefixes lists every known prefix.
func Prefixes() []Prefix {
	return []Prefix{Payout, Beneficiary, Support, Decision, Case, RFI, Approval}
}

// Valid reports whether p is a known prefix.
func (p Prefix) Valid() bool {
	_, ok := widths[p]
	return ok
}

// Format renders prefix and n as a reference. It returns "" for an unknown
// prefix or a number below 1, which no sequence produces; callers format
// numbers they just read from a sequence, so an empty result is a bug to
// surface in review rather than an error to handle on every call.
func Format(prefix Prefix, n int64) string {
	w, ok := widths[prefix]
	if !ok || n < 1 {
		return ""
	}
	return fmt.Sprintf("%s-%0*d", prefix, w, n)
}

// Parse is the strict inverse of Format. It rejects lower case, unknown
// prefixes, signs, whitespace, numbers shorter than the prefix's width, leading
// zeros beyond the width, and zero.
func Parse(s string) (Prefix, int64, error) {
	name, digits, ok := strings.Cut(s, "-")
	if !ok {
		return "", 0, fmt.Errorf("%w: %q has no separator", ErrInvalid, s)
	}
	prefix := Prefix(name)
	w, known := widths[prefix]
	if !known {
		return "", 0, fmt.Errorf("%w: unknown prefix %q", ErrInvalid, name)
	}
	if len(digits) < w {
		return "", 0, fmt.Errorf("%w: %q has fewer than %d digits", ErrInvalid, s, w)
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return "", 0, fmt.Errorf("%w: %q has a non-digit", ErrInvalid, s)
		}
	}
	// Beyond the minimum width a leading zero means the text is not what Format
	// would have produced, so two spellings of one reference cannot exist.
	if len(digits) > w && digits[0] == '0' {
		return "", 0, fmt.Errorf("%w: %q has leading zeros beyond width %d", ErrInvalid, s, w)
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("%w: %q out of range", ErrInvalid, s)
	}
	if n < 1 {
		return "", 0, fmt.Errorf("%w: %q is zero", ErrInvalid, s)
	}
	return prefix, n, nil
}

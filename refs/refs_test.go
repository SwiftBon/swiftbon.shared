package refs_test

import (
	"errors"
	"math"
	"testing"

	"swiftbon.com/shared/refs"
)

func TestFormat(t *testing.T) {
	tests := []struct {
		p    refs.Prefix
		n    int64
		want string
	}{
		{refs.Payout, 20896, "PO-20896"},
		{refs.Payout, 7, "PO-00007"},
		{refs.Payout, 1234567, "PO-1234567"},
		{refs.Beneficiary, 3391, "BEN-3391"},
		{refs.Support, 2291, "SUP-2291"},
		{refs.Decision, 1184, "DEC-1184"},
		{refs.Case, 4182, "CASE-4182"},
		{refs.RFI, 12, "RFI-0012"},
		{refs.Approval, 1001, "APR-1001"},
		{refs.Approval, 123456, "APR-123456"},
		{"XX", 5, ""},
		{refs.Payout, 0, ""},
		{refs.Payout, -3, ""},
	}
	for _, tc := range tests {
		if got := refs.Format(tc.p, tc.n); got != tc.want {
			t.Errorf("Format(%s, %d) = %q, want %q", tc.p, tc.n, got, tc.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, p := range refs.Prefixes() {
		for _, n := range []int64{1, 9, 99, 1000, 9999, 10000, 20896, 99999, 100000, math.MaxInt64} {
			s := refs.Format(p, n)
			gp, gn, err := refs.Parse(s)
			if err != nil || gp != p || gn != n {
				t.Errorf("%s -> %v %d %v, want %v %d", s, gp, gn, err, p, n)
			}
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{
		"", "PO", "PO-", "-20896", "po-20896", "Po-20896", "XX-1234", "PO_20896",
		"PO-2089", "BEN-339", "PO-020896", "BEN-03391", "PO-00000", "BEN-0000",
		"PO-2089a", "PO-+20896", "PO--20896", "PO- 20896", " PO-20896", "PO-20896 ",
		"PO-20896-1", "PO-9223372036854775808", "PO-٢٠٨٩٦",
	} {
		_, _, err := refs.Parse(s)
		if err == nil {
			t.Errorf("Parse(%q) accepted", s)
		} else if !errors.Is(err, refs.ErrInvalid) {
			t.Errorf("Parse(%q) error %v does not match ErrInvalid", s, err)
		}
	}
}

func TestParseAcceptsPaddedMinimum(t *testing.T) {
	// Exactly at the width a leading zero is what Format produces.
	p, n, err := refs.Parse("PO-00007")
	if err != nil || p != refs.Payout || n != 7 {
		t.Errorf("got %v %d %v", p, n, err)
	}
}

func TestPrefixValid(t *testing.T) {
	if !refs.Payout.Valid() || refs.Prefix("ZZ").Valid() {
		t.Error("Valid() wrong")
	}
}

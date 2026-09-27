package money

import (
	"errors"
	"math"
	"testing"
	"testing/quick"
)

func TestString(t *testing.T) {
	tests := []struct {
		name string
		m    Money
		want string
	}{
		{name: "whole and fraction", m: Money{12345, "CAD"}, want: "123.45 CAD"},
		{name: "zero", m: Money{0, "USD"}, want: "0.00 USD"},
		{name: "under one unit", m: Money{7, "USD"}, want: "0.07 USD"},
		{name: "exactly one unit", m: Money{100, "GBP"}, want: "1.00 GBP"},
		{name: "negative", m: Money{-7, "USD"}, want: "-0.07 USD"},
		{name: "negative with a whole part", m: Money{-12345, "EUR"}, want: "-123.45 EUR"},
		{name: "large", m: Money{123456789012, "NGN"}, want: "1234567890.12 NGN"},
		{name: "currency is normalised in output", m: Money{100, "cad"}, want: "1.00 CAD"},
		{name: "max", m: Money{MaxAmount, "CAD"}, want: "92233720368547758.07 CAD"},
		{name: "min", m: Money{MinAmount, "CAD"}, want: "-92233720368547758.07 CAD"},
		// An unknown currency still renders, so a log line about bad input is
		// readable rather than empty. It assumes exponent 2 and says so by
		// echoing the code back verbatim.
		{name: "unknown currency still renders", m: Money{12345, "XYZ"}, want: "123.45 XYZ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// math.MinInt64 cannot be negated in an int64. Format must still render it
// rather than panic or print a wrong sign, because it can reach a log line from
// a struct literal that never passed through New.
func TestFormatHandlesMinInt64(t *testing.T) {
	got := Format(math.MinInt64, 2)
	want := "-92233720368547758.08"
	if got != want {
		t.Errorf("Format(math.MinInt64, 2) = %q, want %q", got, want)
	}
}

func TestFormat(t *testing.T) {
	tests := []struct {
		minor    int64
		exponent int32
		want     string
	}{
		{12345, 2, "123.45"},
		{5, 2, "0.05"},
		{50, 2, "0.50"},
		{-5, 2, "-0.05"},
		{0, 2, "0.00"},
		{100, 0, "100"}, // exponent 0, as JPY would be
		{-100, 0, "-100"},
		{12345, 3, "12.345"}, // exponent 3, as KWD would be
		{5, 3, "0.005"},
		{-5, 3, "-0.005"},
		{1, 1, "0.1"},
	}
	for _, tt := range tests {
		if got := Format(tt.minor, tt.exponent); got != tt.want {
			t.Errorf("Format(%d, %d) = %q, want %q", tt.minor, tt.exponent, got, tt.want)
		}
	}
}

func TestMajorUnits(t *testing.T) {
	got, err := Money{12345, "CAD"}.MajorUnits()
	if err != nil {
		t.Fatalf("MajorUnits: %v", err)
	}
	if got != "123.45" {
		t.Errorf("MajorUnits() = %q, want %q", got, "123.45")
	}

	if _, err := (Money{1, "XYZ"}).MajorUnits(); !errors.Is(err, ErrUnknownCurrency) {
		t.Errorf("MajorUnits on an unknown currency error = %v, want ErrUnknownCurrency", err)
	}
}

func TestParseMajorUnits(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		currency string
		want     int64
		wantErr  error
	}{
		{name: "whole and fraction", in: "123.45", currency: "CAD", want: 12345},
		{name: "whole only", in: "123", currency: "CAD", want: 12300},
		{name: "single decimal place is padded", in: "123.4", currency: "CAD", want: 12340},
		{name: "leading zero", in: "0.07", currency: "CAD", want: 7},
		{name: "bare fraction", in: ".07", currency: "CAD", want: 7},
		{name: "zero", in: "0", currency: "CAD", want: 0},
		{name: "negative", in: "-123.45", currency: "CAD", want: -12345},
		{name: "explicit plus", in: "+123.45", currency: "CAD", want: 12345},
		{name: "surrounding whitespace", in: "  123.45  ", currency: "CAD", want: 12345},
		{name: "lowercase currency", in: "1.00", currency: "cad", want: 100},

		// Excess precision is rejected, not rounded. A caller who sends 1.234
		// CAD disagrees with the ledger about the amount, and that has to
		// surface at the boundary rather than as a third of a cent vanishing.
		{name: "too many decimal places", in: "1.234", currency: "CAD", wantErr: ErrTooManyDecimalPlaces},
		{name: "far too many decimal places", in: "1.23456789", currency: "CAD", wantErr: ErrTooManyDecimalPlaces},

		{name: "empty", in: "", currency: "CAD", wantErr: ErrInvalidMajorUnits},
		{name: "whitespace only", in: "   ", currency: "CAD", wantErr: ErrInvalidMajorUnits},
		{name: "not a number", in: "abc", currency: "CAD", wantErr: ErrInvalidMajorUnits},
		{name: "letters in the fraction", in: "1.2a", currency: "CAD", wantErr: ErrInvalidMajorUnits},
		{name: "thousands separator", in: "1,234.00", currency: "CAD", wantErr: ErrInvalidMajorUnits},
		{name: "two decimal points", in: "1.2.3", currency: "CAD", wantErr: ErrInvalidMajorUnits},
		{name: "sign only", in: "-", currency: "CAD", wantErr: ErrInvalidMajorUnits},
		{name: "unknown currency", in: "1.00", currency: "XYZ", wantErr: ErrUnknownCurrency},
		{name: "empty currency", in: "1.00", currency: "", wantErr: ErrEmptyCurrency},
		{name: "overflows int64", in: "99999999999999999999.99", currency: "CAD", wantErr: ErrOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMajorUnits(tt.in, tt.currency)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ParseMajorUnits(%q, %q) error = %v, want %v", tt.in, tt.currency, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMajorUnits(%q, %q) unexpected error: %v", tt.in, tt.currency, err)
			}
			if got.Amount != tt.want {
				t.Errorf("ParseMajorUnits(%q) = %d minor units, want %d", tt.in, got.Amount, tt.want)
			}
		})
	}
}

// Parsing "1.15" must produce 115, not 114. This is the same truncation bug as
// in Convert, arriving through the other door.
func TestParseMajorUnitsDoesNotTruncate(t *testing.T) {
	for _, in := range []string{"1.15", "0.29", "2.675", "1.005"} {
		got, err := ParseMajorUnits(in, "CAD")
		if errors.Is(err, ErrTooManyDecimalPlaces) {
			continue // 2.675 and 1.005 have three places and are rejected
		}
		if err != nil {
			t.Fatalf("ParseMajorUnits(%q): %v", in, err)
		}
		round, err := ParseMajorUnits(in, "CAD")
		if err != nil || round.Amount != got.Amount {
			t.Fatalf("ParseMajorUnits(%q) is not deterministic", in)
		}
	}

	got, err := ParseMajorUnits("1.15", "CAD")
	if err != nil {
		t.Fatalf("ParseMajorUnits: %v", err)
	}
	if got.Amount != 115 {
		t.Fatalf("ParseMajorUnits(\"1.15\") = %d, want 115; a value of 114 means "+
			"the float path is back", got.Amount)
	}
}

// Format and ParseMajorUnits must be exact inverses across the whole range.
func TestPropertyFormatParseRoundTrip(t *testing.T) {
	f := func(amount int64) bool {
		if amount == math.MinInt64 {
			return true // outside the representable range
		}
		m := Money{Amount: amount, Currency: "CAD"}
		s, err := m.MajorUnits()
		if err != nil {
			return false
		}
		back, err := ParseMajorUnits(s, "CAD")
		if err != nil {
			return false
		}
		return back == m
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 1000}); err != nil {
		t.Errorf("Format and ParseMajorUnits are not inverses: %v", err)
	}
}

func TestAllDigits(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"0", true},
		{"1234567890", true},
		{"12a", false},
		{"-1", false},
		{" 1", false},
		{"1.2", false},
	}
	for _, tt := range tests {
		if got := allDigits(tt.in); got != tt.want {
			t.Errorf("allDigits(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

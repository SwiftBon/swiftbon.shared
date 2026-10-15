package money

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/shopspring/decimal"
)

// ErrInvalidMinorUnits is returned by ParseMinor for anything that is not a
// plain integer count of minor units.
var ErrInvalidMinorUnits = errors.New("money: invalid minor-unit amount")

// Equivalent expresses m in the target currency at midRate, which is the mid
// price of one unit of m.Currency in the target currency. It uses the same
// half-up rounding as Convert.
//
// It is for thresholds and indicative totals ONLY: "is this payout above the
// $10,000-equivalent approval limit", "total position across wallets". It
// prices at mid with no spread, so the number is not one a customer can trade
// at. It must never be used to size a posting or settle anything; settlement
// goes through an FX quote and Convert.
//
// If m is already in the target currency it is returned unchanged and midRate
// is ignored.
func Equivalent(m Money, target string, midRate decimal.Decimal) (Money, error) {
	to, err := Lookup(target)
	if err != nil {
		return Money{}, err
	}
	if err := m.Validate(); err != nil {
		return Money{}, err
	}
	if m.Currency == to.Code {
		return m, nil
	}
	converted, _, err := Convert(m, to, midRate)
	if err != nil {
		return Money{}, fmt.Errorf("money: equivalent: %w", err)
	}
	return converted, nil
}

// ParseMinor reads a minor-unit integer such as "12345" or "-250" into Money.
// It is the inverse of strconv.FormatInt(m.Amount, 10), for reading amounts
// back out of the CSV export.
//
// It is exact and strict: digits with an optional leading '-' only. A decimal
// point, exponent, sign '+', whitespace or thousands separator is an error
// rather than something to guess at, because a "12.50" read as 1250 minor units
// in one place and 12 in another is how a statement stops tying out.
func ParseMinor(s string, currency string) (Money, error) {
	c, err := Lookup(currency)
	if err != nil {
		return Money{}, err
	}
	digits := s
	if len(digits) > 0 && digits[0] == '-' {
		digits = digits[1:]
	}
	if digits == "" || !allDigits(digits) {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidMinorUnits, s)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %q", ErrOverflow, s)
	}
	return New(n, c.Code)
}

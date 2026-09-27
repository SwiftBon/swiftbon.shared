package money

import (
	"strconv"
	"strings"
)

// String renders the amount for logs and errors: "123.45 CAD", "-0.07 USD".
//
// This is a diagnostic format, not a user-facing one. It has no currency
// symbol, no thousands separator and no locale awareness, precisely so that it
// is unambiguous in a log line and never mistaken for something safe to render
// in a UI. Present amounts to customers through the client's own locale
// formatting, from Amount and Currency.
func (m Money) String() string {
	exp := int32(2)
	if c, err := Lookup(m.Currency); err == nil {
		exp = c.Exponent
	}
	return Format(m.Amount, exp) + " " + Normalise(m.Currency)
}

// Format renders a minor-unit amount as a decimal string with exponent places.
// It is exact: the digits are placed by string manipulation, never by dividing
// through a float.
func Format(minor int64, exponent int32) string {
	if exponent <= 0 {
		return strconv.FormatInt(minor, 10)
	}

	neg := minor < 0
	// Take the magnitude via strings rather than negation, so math.MinInt64 —
	// whose negation does not fit in an int64 — still formats correctly.
	digits := strconv.FormatInt(minor, 10)
	if neg {
		digits = digits[1:]
	}

	places := int(exponent)
	if len(digits) <= places {
		digits = strings.Repeat("0", places-len(digits)+1) + digits
	}
	split := len(digits) - places
	out := digits[:split] + "." + digits[split:]
	if neg {
		out = "-" + out
	}
	return out
}

// MajorUnits renders m in major units without the currency code: "123.45".
// For display pipelines that supply their own symbol.
func (m Money) MajorUnits() (string, error) {
	c, err := Lookup(m.Currency)
	if err != nil {
		return "", err
	}
	return Format(m.Amount, c.Exponent), nil
}

// ParseMajorUnits reads a major-unit decimal string — "123.45" — into minor
// units for the given currency.
//
// It rejects anything with more decimal places than the currency has, rather
// than rounding. A caller who sends "1.234" CAD has either a bug or a different
// idea of the amount than the ledger does, and both need to surface as an error
// at the boundary rather than as a third of a cent quietly disappearing.
func ParseMajorUnits(s, currency string) (Money, error) {
	c, err := Lookup(currency)
	if err != nil {
		return Money{}, err
	}

	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return Money{}, ErrInvalidMajorUnits
	}

	neg := false
	switch trimmed[0] {
	case '-':
		neg, trimmed = true, trimmed[1:]
	case '+':
		trimmed = trimmed[1:]
	}

	whole, frac, hasFrac := strings.Cut(trimmed, ".")
	if whole == "" && !hasFrac {
		return Money{}, ErrInvalidMajorUnits
	}
	if whole == "" {
		whole = "0"
	}
	// Shape before precision: "1.2.3" is malformed, not over-precise, and
	// saying so points the caller at the actual problem.
	if !allDigits(whole) || (hasFrac && !allDigits(frac)) {
		return Money{}, ErrInvalidMajorUnits
	}
	if hasFrac && len(frac) > int(c.Exponent) {
		return Money{}, ErrTooManyDecimalPlaces
	}

	frac += strings.Repeat("0", int(c.Exponent)-len(frac))
	minor, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return Money{}, ErrOverflow
	}
	if neg {
		minor = -minor
	}
	return New(minor, c.Code)
}

func allDigits(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

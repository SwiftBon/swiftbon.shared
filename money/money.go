// Package money is the single definition of what an amount is at Swiftbon.
//
// Amounts are exact int64 counts of a currency's minor unit. There is no float
// and no decimal anywhere in an amount: floats cannot represent 0.10 and
// decimals invite implicit rounding at every arithmetic step. Rates are the one
// place a decimal appears (see convert.go), and rate application is the one
// place rounding happens — explicitly, with the residual handed back to the
// caller so the double entry still balances.
//
// Every operation that could combine two currencies returns an error instead.
// Nothing here panics on bad input and nothing coerces: a CAD amount added to a
// USD balance is a bug that must surface at the call site, not a number that
// quietly continues down the money path.
package money

import (
	"errors"
	"fmt"
	"math"
)

var (
	// ErrCurrencyMismatch is returned when an operation is asked to combine
	// two different currencies. Callers must convert explicitly first.
	ErrCurrencyMismatch = errors.New("money: currency mismatch")

	// ErrUnknownCurrency is returned for a code absent from the registry.
	ErrUnknownCurrency = errors.New("money: unknown currency")

	// ErrEmptyCurrency is returned for a missing or blank currency code.
	ErrEmptyCurrency = errors.New("money: empty currency")

	// ErrOverflow is returned when a result cannot be represented in int64.
	// It is returned instead of wrapping, because a wrapped balance is a
	// balance with the wrong sign.
	ErrOverflow = errors.New("money: int64 overflow")

	// ErrAmountOutOfRange is returned for math.MinInt64, the one int64 value
	// this package refuses to hold. See MinAmount.
	ErrAmountOutOfRange = errors.New("money: amount out of range")
)

// MaxAmount and MinAmount bound the representable amount. The domain is
// deliberately symmetric — it excludes math.MinInt64, whose negation does not
// fit in an int64 — so that Neg is exact for every value a Money can hold and
// needs no error return. In minor units this still spans roughly ±92 quadrillion
// major units, so nothing real is excluded.
const (
	MaxAmount int64 = math.MaxInt64
	MinAmount int64 = -math.MaxInt64
)

// Money is an exact amount in the minor units of Currency.
// {Amount: 12345, Currency: "CAD"} is $123.45.
type Money struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// New builds a Money, validating and normalising the currency code.
// A lowercase or padded code is accepted and canonicalised; an unknown or empty
// one is an error.
func New(amount int64, currency string) (Money, error) {
	c, err := Lookup(currency)
	if err != nil {
		return Money{}, err
	}
	if amount == math.MinInt64 {
		return Money{}, fmt.Errorf("%w: %d", ErrAmountOutOfRange, amount)
	}
	return Money{Amount: amount, Currency: c.Code}, nil
}

// Zero returns the zero amount in currency. The code is normalised but not
// validated — Zero is for call sites that already hold a checked currency.
// Validate on the result if the code came from outside.
func Zero(currency string) Money {
	return Money{Amount: 0, Currency: Normalise(currency)}
}

// Validate reports whether m is a well-formed amount in a supported currency.
func (m Money) Validate() error {
	if _, err := Lookup(m.Currency); err != nil {
		return err
	}
	if m.Amount == math.MinInt64 {
		return fmt.Errorf("%w: %d", ErrAmountOutOfRange, m.Amount)
	}
	if m.Currency != Normalise(m.Currency) {
		return fmt.Errorf("money: currency %q is not normalised", m.Currency)
	}
	return nil
}

// sameCurrency is the guard in front of every binary operation.
func (m Money) sameCurrency(o Money) error {
	if Normalise(m.Currency) != Normalise(o.Currency) {
		return fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.Currency, o.Currency)
	}
	return nil
}

// Add returns m+o. It errors if the currencies differ or if the result does not
// fit in the representable range.
func (m Money) Add(o Money) (Money, error) {
	if err := m.sameCurrency(o); err != nil {
		return Money{}, err
	}
	sum, err := addInt64(m.Amount, o.Amount)
	if err != nil {
		return Money{}, err
	}
	return Money{Amount: sum, Currency: m.Currency}, nil
}

// Sub returns m-o. It errors if the currencies differ or if the result does not
// fit in the representable range.
func (m Money) Sub(o Money) (Money, error) {
	if err := m.sameCurrency(o); err != nil {
		return Money{}, err
	}
	diff, err := subInt64(m.Amount, o.Amount)
	if err != nil {
		return Money{}, err
	}
	return Money{Amount: diff, Currency: m.Currency}, nil
}

// Neg returns -m. It is exact for every value Money can hold, because the
// representable range excludes math.MinInt64.
func (m Money) Neg() Money {
	return Money{Amount: -m.Amount, Currency: m.Currency}
}

// IsZero reports whether the amount is exactly zero.
func (m Money) IsZero() bool { return m.Amount == 0 }

// IsNegative reports whether the amount is strictly less than zero.
func (m Money) IsNegative() bool { return m.Amount < 0 }

// IsPositive reports whether the amount is strictly greater than zero.
func (m Money) IsPositive() bool { return m.Amount > 0 }

// Cmp compares m against o, returning -1, 0 or +1. It errors if the currencies
// differ: there is no ordering between amounts in different currencies without
// a rate, and inventing one here would let a balance check pass against the
// wrong denomination.
func (m Money) Cmp(o Money) (int, error) {
	if err := m.sameCurrency(o); err != nil {
		return 0, err
	}
	switch {
	case m.Amount < o.Amount:
		return -1, nil
	case m.Amount > o.Amount:
		return 1, nil
	default:
		return 0, nil
	}
}

// addInt64 adds with an explicit overflow check, keeping the result inside the
// symmetric representable range.
func addInt64(a, b int64) (int64, error) {
	if b > 0 && a > MaxAmount-b {
		return 0, fmt.Errorf("%w: %d + %d", ErrOverflow, a, b)
	}
	if b < 0 && a < MinAmount-b {
		return 0, fmt.Errorf("%w: %d + %d", ErrOverflow, a, b)
	}
	return a + b, nil
}

// subInt64 subtracts with an explicit overflow check. It does not negate b
// first, because negation is itself the operation that can overflow.
func subInt64(a, b int64) (int64, error) {
	if b < 0 && a > MaxAmount+b {
		return 0, fmt.Errorf("%w: %d - %d", ErrOverflow, a, b)
	}
	if b > 0 && a < MinAmount+b {
		return 0, fmt.Errorf("%w: %d - %d", ErrOverflow, a, b)
	}
	return a - b, nil
}

// Errors raised when parsing human-entered major-unit amounts.
var (
	// ErrInvalidMajorUnits is returned for a string that is not a decimal number.
	ErrInvalidMajorUnits = errors.New("money: invalid major-unit amount")

	// ErrTooManyDecimalPlaces is returned when an amount carries more precision
	// than the currency has minor units, rather than rounding it away.
	ErrTooManyDecimalPlaces = errors.New("money: more decimal places than the currency supports")
)

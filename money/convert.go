package money

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// Rate is an exchange rate expressed in major units of the destination currency
// per one major unit of the source currency.
//
// Rates are the only decimals in this package. They are never float64: the gap
// between a rate of 1.15 and its nearest float is tiny, but multiplied across a
// day of volume and then truncated it becomes a real, unexplained loss.
type Rate = decimal.Decimal

// ErrInvalidRate is returned for a rate that is zero or negative. A zero rate
// converts every amount to nothing; a negative one flips the sign of a payment.
var ErrInvalidRate = errors.New("money: rate must be positive")

// residualScale is the number of decimal places kept when inverting a rate.
// Wide enough that a round trip lands within one minor unit for any rate the
// desk quotes, narrow enough that the division terminates.
const residualScale = 32

// Convert applies rate to m, returning the destination amount and the rounding
// residual expressed in destination minor units.
//
//	exact     = srcMinor × rate × 10^(destExp − srcExp)
//	destMinor = round_half_up(exact)
//	residual  = exact − destMinor
//
// so that converted + residual reconstructs the exact value with no loss.
//
// Rounding is half-up and explicit. It is never int64(f * 100): that truncation
// is why an inbound 1.15 becomes 114 minor units rather than 115 — 1.15 is not
// representable in binary floating point, so the product lands just below the
// integer and the cast throws the remainder away.
//
// The residual MUST be carried to an FX rounding account rather than dropped.
// Note its magnitude: exact − round(exact) is by construction strictly within
// half a minor unit, so for a single conversion the residual's own Money value
// rounds to zero. The value that was actually discarded lives in the fractional
// part, which is why ConvertExact exists and why Residuals accumulates it — a
// fraction of a cent dropped ten thousand times is a cent times ten thousand
// missing from the FX position. Use Convert when you only need the payable
// amount; use ConvertExact or Residuals anywhere the books must reconcile.
func Convert(m Money, to Currency, rate Rate) (converted Money, residual Money, err error) {
	converted, frac, err := ConvertExact(m, to, rate)
	if err != nil {
		return Money{}, Money{}, err
	}
	// The postable whole minor units of the residual. |frac| is strictly less
	// than one minor unit, so this is always zero for a single conversion —
	// the discarded value lives entirely in the fraction, which is why
	// ConvertExact and Residuals exist. It is computed rather than hardcoded so
	// the relationship between the two forms stays visible.
	return converted, Money{Amount: frac.Truncate(0).IntPart(), Currency: converted.Currency}, nil
}

// ConvertExact is Convert with the residual returned unrounded, as a signed
// decimal count of destination minor units. It is the form to use when the
// books must reconcile, because converted.Amount + residual is exactly equal to
// the true converted value.
func ConvertExact(m Money, to Currency, rate Rate) (converted Money, residual decimal.Decimal, err error) {
	exact, dest, err := exactConversion(m, to, rate)
	if err != nil {
		return Money{}, decimal.Zero, err
	}

	roundedDec := roundHalfUp(exact)
	if !roundedDec.BigInt().IsInt64() {
		return Money{}, decimal.Zero, fmt.Errorf("%w: converting %s at rate %s", ErrOverflow, m, rate)
	}
	rounded := roundedDec.IntPart()
	if rounded == minInt64 {
		return Money{}, decimal.Zero, fmt.Errorf("%w: converting %s at rate %s", ErrAmountOutOfRange, m, rate)
	}

	return Money{Amount: rounded, Currency: dest.Code}, exact.Sub(roundedDec), nil
}

// exactConversion computes the unrounded destination amount in destination
// minor units, and resolves the destination currency from the registry.
func exactConversion(m Money, to Currency, rate Rate) (decimal.Decimal, Currency, error) {
	from, err := Lookup(m.Currency)
	if err != nil {
		return decimal.Zero, Currency{}, err
	}
	if err := m.Validate(); err != nil {
		return decimal.Zero, Currency{}, err
	}
	// Re-resolve the destination from the registry rather than trusting the
	// caller's struct, so a hand-built Currency carrying a wrong Exponent
	// cannot silently rescale the result by a factor of ten.
	dest, err := Lookup(to.Code)
	if err != nil {
		return decimal.Zero, Currency{}, err
	}
	if rate.Sign() <= 0 {
		return decimal.Zero, Currency{}, fmt.Errorf("%w: %s", ErrInvalidRate, rate.String())
	}

	exact := decimal.NewFromInt(m.Amount).
		Mul(rate).
		Shift(dest.Exponent - from.Exponent)
	return exact, dest, nil
}

// roundHalfUp rounds to a whole number, half away from zero: 0.5 becomes 1 and
// −0.5 becomes −1. The rule is named and isolated here so a test can assert it
// directly, rather than leaving it an implicit property of a dependency.
func roundHalfUp(d decimal.Decimal) decimal.Decimal {
	return d.Round(0)
}

// minInt64 mirrors math.MinInt64, the one value Money refuses to hold.
const minInt64 = -1 << 63

// Invert returns 1/rate, for converting back. The result is exact only when
// rate is a power of ten; otherwise a round trip lands within one minor unit,
// which is the guarantee the tests assert.
func Invert(rate Rate) (Rate, error) {
	if rate.Sign() <= 0 {
		return decimal.Zero, fmt.Errorf("%w: %s", ErrInvalidRate, rate.String())
	}
	return decimal.NewFromInt(1).DivRound(rate, residualScale), nil
}

// Residuals accumulates the fractional minor units that individual conversions
// discard, and hands back whole minor units once they add up to something
// postable.
//
// This is the mechanism that keeps sub-cent rounding from leaking value. Each
// conversion contributes a fraction; when the running total crosses one minor
// unit, Drain returns that unit for posting to the FX rounding account and
// keeps the remainder. Nothing is ever thrown away, and the amount posted is
// always a whole minor unit the ledger can actually hold.
//
// A Residuals is not safe for concurrent use; give each posting batch its own.
type Residuals struct {
	currency string
	total    decimal.Decimal
}

// NewResiduals starts an accumulator for a currency. The code is validated.
func NewResiduals(currency string) (*Residuals, error) {
	c, err := Lookup(currency)
	if err != nil {
		return nil, err
	}
	return &Residuals{currency: c.Code, total: decimal.Zero}, nil
}

// Add folds a residual, as returned by ConvertExact, into the running total.
// The residual must be denominated in the accumulator's currency.
func (r *Residuals) Add(currency string, residual decimal.Decimal) error {
	if Normalise(currency) != r.currency {
		return fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, currency, r.currency)
	}
	r.total = r.total.Add(residual)
	return nil
}

// Pending returns the un-posted total, in fractional destination minor units.
func (r *Residuals) Pending() decimal.Decimal { return r.total }

// Drain returns the whole minor units accumulated so far, ready to post, and
// retains the sub-unit remainder for the next batch. Truncation is toward zero
// so the retained remainder always keeps the sign of what is still owed.
func (r *Residuals) Drain() (Money, error) {
	whole := r.total.Truncate(0)
	if !whole.BigInt().IsInt64() {
		return Money{}, fmt.Errorf("%w: residual total %s", ErrOverflow, r.total)
	}
	amount := whole.IntPart()
	if amount == minInt64 {
		return Money{}, fmt.Errorf("%w: residual total %s", ErrAmountOutOfRange, r.total)
	}
	r.total = r.total.Sub(whole)
	return Money{Amount: amount, Currency: r.currency}, nil
}

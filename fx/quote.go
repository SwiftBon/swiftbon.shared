// Package fx holds the quote and rate types shared between the FX service that
// produces a quote and the postings service that consumes one.
//
// The two live in separate repositories, and a quote is the object they must
// agree about exactly: which side is sold, which is bought, what rate was
// honoured, and when it stops being honoured. Defining it once here is what
// stops the producer's idea of a quote and the consumer's drifting apart, in
// the same way the AccountType enum already has.
package fx

import (
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"swiftbon.com/shared/money"
)

// Errors returned by Quote.Validate.
var (
	ErrMissingID        = errors.New("fx: quote id is required")
	ErrSameCurrency     = errors.New("fx: buy and sell currencies must differ")
	ErrNonPositiveSide  = errors.New("fx: buy and sell amounts must be positive")
	ErrNonPositiveRate  = errors.New("fx: rate must be positive")
	ErrNegativeSpread   = errors.New("fx: spread must not be negative")
	ErrMissingSource    = errors.New("fx: rate source is required")
	ErrExpiryNotAfter   = errors.New("fx: expiry must be after creation")
	ErrMissingTimestamp = errors.New("fx: created and expiry timestamps are required")
)

// Quote is a firm price to exchange one currency for another, valid until it
// expires.
//
// Sell is what the customer gives up and Buy is what they receive; naming the
// two sides rather than calling them "from" and "to" keeps the direction
// unambiguous on both sides of the wire. Rate is the price actually honoured —
// MidRate adjusted by SpreadBps — and is the number the postings must use.
// MidRate and SpreadBps are carried alongside it so the margin on a trade can
// be reconstructed after the fact without re-querying the rate provider.
type Quote struct {
	ID           string
	Sell         money.Money
	Buy          money.Money
	MidRate      decimal.Decimal
	Rate         decimal.Decimal // mid adjusted by spread
	SpreadBps    int32
	RateSource   string
	SourceRateID string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// IsExpired reports whether the quote has stopped being honourable at now.
//
// The boundary is inclusive: a quote is expired at exactly its expiry instant,
// not a moment after. Honouring a price for even an instant past its stated
// life is how a stale rate reaches a posting.
func (q Quote) IsExpired(now time.Time) bool {
	return !now.Before(q.ExpiresAt)
}

// Validate checks that the quote is internally coherent and safe to post
// against: both currencies supported, both amounts positive, the two sides in
// different currencies, a positive rate and a lifetime that makes sense.
//
// It deliberately does not check the rate against the amounts. A quote may
// carry fees or a tiered price, so Buy is not always exactly Sell × Rate, and
// asserting that here would reject legitimate quotes.
func (q Quote) Validate() error {
	if q.ID == "" {
		return ErrMissingID
	}
	if err := q.Sell.Validate(); err != nil {
		return fmt.Errorf("fx: sell side: %w", err)
	}
	if err := q.Buy.Validate(); err != nil {
		return fmt.Errorf("fx: buy side: %w", err)
	}
	if !q.Sell.IsPositive() || !q.Buy.IsPositive() {
		return fmt.Errorf("%w: sell %s buy %s", ErrNonPositiveSide, q.Sell, q.Buy)
	}
	if money.Normalise(q.Sell.Currency) == money.Normalise(q.Buy.Currency) {
		return fmt.Errorf("%w: both %s", ErrSameCurrency, q.Sell.Currency)
	}
	if q.Rate.Sign() <= 0 {
		return fmt.Errorf("%w: %s", ErrNonPositiveRate, q.Rate)
	}
	if q.MidRate.Sign() <= 0 {
		return fmt.Errorf("%w: mid %s", ErrNonPositiveRate, q.MidRate)
	}
	if q.SpreadBps < 0 {
		return fmt.Errorf("%w: %d", ErrNegativeSpread, q.SpreadBps)
	}
	if q.RateSource == "" {
		return ErrMissingSource
	}
	if q.CreatedAt.IsZero() || q.ExpiresAt.IsZero() {
		return ErrMissingTimestamp
	}
	if !q.ExpiresAt.After(q.CreatedAt) {
		return fmt.Errorf("%w: created %s expires %s",
			ErrExpiryNotAfter, q.CreatedAt.Format(time.RFC3339), q.ExpiresAt.Format(time.RFC3339))
	}
	return nil
}

// Pair returns the currency pair as "SELL/BUY", for logs and metric labels.
func (q Quote) Pair() string {
	return money.Normalise(q.Sell.Currency) + "/" + money.Normalise(q.Buy.Currency)
}

// TimeToLive returns how long the quote remains honourable from now. It is zero
// once expired, never negative, so callers can compare it against a threshold
// without a separate sign check.
func (q Quote) TimeToLive(now time.Time) time.Duration {
	if q.IsExpired(now) {
		return 0
	}
	return q.ExpiresAt.Sub(now)
}

// ApplySpread returns mid adjusted by spreadBps in the direction that favours
// the book: the customer receives less than mid.
//
// One basis point is one hundredth of a percent, so the adjustment is
// mid × (1 − spreadBps/10000). The arithmetic is decimal throughout, because a
// spread applied in float is a spread that is not quite the spread that was
// quoted.
func ApplySpread(mid decimal.Decimal, spreadBps int32) (decimal.Decimal, error) {
	if mid.Sign() <= 0 {
		return decimal.Zero, fmt.Errorf("%w: mid %s", ErrNonPositiveRate, mid)
	}
	if spreadBps < 0 {
		return decimal.Zero, fmt.Errorf("%w: %d", ErrNegativeSpread, spreadBps)
	}
	factor := decimal.NewFromInt(1).Sub(
		decimal.NewFromInt(int64(spreadBps)).Div(decimal.NewFromInt(10000)),
	)
	rate := mid.Mul(factor)
	if rate.Sign() <= 0 {
		return decimal.Zero, fmt.Errorf("%w: spread %d bps wipes out mid %s", ErrNonPositiveRate, spreadBps, mid)
	}
	return rate, nil
}

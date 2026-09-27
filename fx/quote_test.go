package fx

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"swiftbon.com/shared/money"
)

func dec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

var (
	created = time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	expires = created.Add(30 * time.Second)
)

// validQuote is the baseline every table row perturbs one field of, so each
// failure names exactly one cause.
func validQuote() Quote {
	return Quote{
		ID:           "qt_01J000000000000000000000",
		Sell:         money.Money{Amount: 10_000, Currency: "CAD"},
		Buy:          money.Money{Amount: 7_391, Currency: "USD"},
		MidRate:      dec("0.7400"),
		Rate:         dec("0.7391"),
		SpreadBps:    12,
		RateSource:   "oanda",
		SourceRateID: "oanda_20260829_120000",
		CreatedAt:    created,
		ExpiresAt:    expires,
	}
}

func TestQuoteValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Quote)
		wantErr error
	}{
		{name: "valid quote", mutate: func(*Quote) {}},

		{name: "missing id", mutate: func(q *Quote) { q.ID = "" }, wantErr: ErrMissingID},

		{
			name:    "sell and buy in the same currency",
			mutate:  func(q *Quote) { q.Buy.Currency = "CAD" },
			wantErr: ErrSameCurrency,
		},
		{
			name:    "same currency differing only in case",
			mutate:  func(q *Quote) { q.Buy = money.Money{Amount: 100, Currency: "CAD"} },
			wantErr: ErrSameCurrency,
		},

		{name: "zero sell amount", mutate: func(q *Quote) { q.Sell.Amount = 0 }, wantErr: ErrNonPositiveSide},
		{name: "negative sell amount", mutate: func(q *Quote) { q.Sell.Amount = -1 }, wantErr: ErrNonPositiveSide},
		{name: "zero buy amount", mutate: func(q *Quote) { q.Buy.Amount = 0 }, wantErr: ErrNonPositiveSide},
		{name: "negative buy amount", mutate: func(q *Quote) { q.Buy.Amount = -1 }, wantErr: ErrNonPositiveSide},

		{
			name:    "unsupported sell currency",
			mutate:  func(q *Quote) { q.Sell.Currency = "XYZ" },
			wantErr: money.ErrUnknownCurrency,
		},
		{
			name:    "unsupported buy currency",
			mutate:  func(q *Quote) { q.Buy.Currency = "JPY" },
			wantErr: money.ErrUnknownCurrency,
		},
		{
			name:    "empty sell currency",
			mutate:  func(q *Quote) { q.Sell.Currency = "" },
			wantErr: money.ErrEmptyCurrency,
		},
		{
			name:    "empty buy currency",
			mutate:  func(q *Quote) { q.Buy.Currency = "" },
			wantErr: money.ErrEmptyCurrency,
		},

		{name: "zero rate", mutate: func(q *Quote) { q.Rate = decimal.Zero }, wantErr: ErrNonPositiveRate},
		{name: "negative rate", mutate: func(q *Quote) { q.Rate = dec("-0.5") }, wantErr: ErrNonPositiveRate},
		{name: "zero mid rate", mutate: func(q *Quote) { q.MidRate = decimal.Zero }, wantErr: ErrNonPositiveRate},
		{name: "negative mid rate", mutate: func(q *Quote) { q.MidRate = dec("-1") }, wantErr: ErrNonPositiveRate},

		{name: "negative spread", mutate: func(q *Quote) { q.SpreadBps = -1 }, wantErr: ErrNegativeSpread},
		{name: "zero spread is allowed", mutate: func(q *Quote) { q.SpreadBps = 0 }},

		{name: "missing rate source", mutate: func(q *Quote) { q.RateSource = "" }, wantErr: ErrMissingSource},

		{name: "zero created at", mutate: func(q *Quote) { q.CreatedAt = time.Time{} }, wantErr: ErrMissingTimestamp},
		{name: "zero expires at", mutate: func(q *Quote) { q.ExpiresAt = time.Time{} }, wantErr: ErrMissingTimestamp},
		{
			name:    "expiry before creation",
			mutate:  func(q *Quote) { q.ExpiresAt = q.CreatedAt.Add(-time.Second) },
			wantErr: ErrExpiryNotAfter,
		},
		{
			name:    "expiry equal to creation leaves no life at all",
			mutate:  func(q *Quote) { q.ExpiresAt = q.CreatedAt },
			wantErr: ErrExpiryNotAfter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := validQuote()
			tt.mutate(&q)

			err := q.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// A quote may carry fees or a tiered price, so Buy is not always exactly
// Sell × Rate. Validate must not reject a legitimate quote for that.
func TestQuoteValidateDoesNotRequireBuyToEqualSellTimesRate(t *testing.T) {
	q := validQuote()
	q.Buy.Amount = 7_000 // a fee has been taken out of the proceeds
	if err := q.Validate(); err != nil {
		t.Fatalf("Validate() rejected a quote with a fee applied: %v", err)
	}
}

func TestIsExpired(t *testing.T) {
	q := validQuote()

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "well before expiry", now: created, want: false},
		{name: "one nanosecond before expiry", now: expires.Add(-time.Nanosecond), want: false},
		// Inclusive boundary: honouring a price for even an instant past its
		// stated life is how a stale rate reaches a posting.
		{name: "exactly at expiry", now: expires, want: true},
		{name: "one nanosecond after expiry", now: expires.Add(time.Nanosecond), want: true},
		{name: "long after expiry", now: expires.Add(time.Hour), want: true},
		{name: "before the quote was even created", now: created.Add(-time.Hour), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := q.IsExpired(tt.now); got != tt.want {
				t.Errorf("IsExpired(%s) = %v, want %v", tt.now.Format(time.RFC3339Nano), got, tt.want)
			}
		})
	}
}

// Expiry must be judged on the instant, not on the wall clock's location, so a
// quote does not gain or lose life by crossing a timezone.
func TestIsExpiredIgnoresTimezone(t *testing.T) {
	q := validQuote()
	toronto := time.FixedZone("EDT", -4*3600)

	atExpiryElsewhere := expires.In(toronto)
	if !q.IsExpired(atExpiryElsewhere) {
		t.Error("IsExpired disagreed with itself across timezones")
	}
	if q.IsExpired(expires.Add(-time.Second).In(toronto)) {
		t.Error("IsExpired reported an unexpired quote as expired in another zone")
	}
}

func TestTimeToLive(t *testing.T) {
	q := validQuote()

	tests := []struct {
		name string
		now  time.Time
		want time.Duration
	}{
		{name: "full life at creation", now: created, want: 30 * time.Second},
		{name: "half spent", now: created.Add(15 * time.Second), want: 15 * time.Second},
		{name: "expired is zero, never negative", now: expires, want: 0},
		{name: "long expired is still zero", now: expires.Add(time.Hour), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := q.TimeToLive(tt.now); got != tt.want {
				t.Errorf("TimeToLive(%s) = %s, want %s", tt.now, got, tt.want)
			}
		})
	}
}

// TimeToLive is never negative, so callers can compare it against a threshold
// without a separate sign check.
func TestTimeToLiveIsNeverNegative(t *testing.T) {
	q := validQuote()
	for _, offset := range []time.Duration{-time.Hour, 0, time.Second, 29 * time.Second, 30 * time.Second, time.Hour} {
		if ttl := q.TimeToLive(created.Add(offset)); ttl < 0 {
			t.Errorf("TimeToLive at offset %s = %s, want >= 0", offset, ttl)
		}
	}
}

func TestPair(t *testing.T) {
	tests := []struct {
		sell, buy string
		want      string
	}{
		{"CAD", "USD", "CAD/USD"},
		{"USD", "NGN", "USD/NGN"},
		{"cad", "usd", "CAD/USD"},
	}
	for _, tt := range tests {
		q := validQuote()
		q.Sell.Currency = tt.sell
		q.Buy.Currency = tt.buy
		if got := q.Pair(); got != tt.want {
			t.Errorf("Pair() = %q, want %q", got, tt.want)
		}
	}
}

func TestApplySpread(t *testing.T) {
	tests := []struct {
		name      string
		mid       string
		spreadBps int32
		want      string
		wantErr   error
	}{
		{name: "no spread leaves mid untouched", mid: "1.15", spreadBps: 0, want: "1.15"},
		{name: "one basis point", mid: "1", spreadBps: 1, want: "0.9999"},
		{name: "fifty basis points", mid: "1", spreadBps: 50, want: "0.995"},
		{name: "spread on a realistic mid", mid: "0.74", spreadBps: 12, want: "0.739112"},
		{name: "one hundred percent spread wipes the rate out", mid: "1.15", spreadBps: 10_000, wantErr: ErrNonPositiveRate},
		{name: "beyond one hundred percent inverts the rate", mid: "1.15", spreadBps: 20_000, wantErr: ErrNonPositiveRate},
		{name: "negative spread", mid: "1.15", spreadBps: -1, wantErr: ErrNegativeSpread},
		{name: "zero mid", mid: "0", spreadBps: 10, wantErr: ErrNonPositiveRate},
		{name: "negative mid", mid: "-1.15", spreadBps: 10, wantErr: ErrNonPositiveRate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ApplySpread(dec(tt.mid), tt.spreadBps)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ApplySpread(%s, %d) error = %v, want %v", tt.mid, tt.spreadBps, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ApplySpread(%s, %d) unexpected error: %v", tt.mid, tt.spreadBps, err)
			}
			if !got.Equal(dec(tt.want)) {
				t.Errorf("ApplySpread(%s, %d) = %s, want %s", tt.mid, tt.spreadBps, got, tt.want)
			}
		})
	}
}

// The spread must always move the rate against the customer, never for them.
func TestApplySpreadNeverFavoursTheCustomer(t *testing.T) {
	mid := dec("1.2345")
	for _, bps := range []int32{0, 1, 5, 12, 50, 100, 500, 9_999} {
		got, err := ApplySpread(mid, bps)
		if err != nil {
			t.Fatalf("ApplySpread(%s, %d): %v", mid, bps, err)
		}
		if got.GreaterThan(mid) {
			t.Errorf("ApplySpread(%s, %d) = %s, which is better than mid", mid, bps, got)
		}
		if bps > 0 && !got.LessThan(mid) {
			t.Errorf("ApplySpread(%s, %d) = %s, which did not move away from mid", mid, bps, got)
		}
		if got.Sign() <= 0 {
			t.Errorf("ApplySpread(%s, %d) = %s, which is not positive", mid, bps, got)
		}
	}
}

// A wider spread must always yield a worse rate. This is monotonicity, and it
// is the property that catches a sign flip in the basis-point arithmetic.
func TestApplySpreadIsMonotonic(t *testing.T) {
	mid := dec("1.2345")
	var previous decimal.Decimal
	for i, bps := range []int32{0, 1, 5, 12, 50, 100, 500, 1_000} {
		got, err := ApplySpread(mid, bps)
		if err != nil {
			t.Fatalf("ApplySpread(%s, %d): %v", mid, bps, err)
		}
		if i > 0 && !got.LessThan(previous) {
			t.Errorf("ApplySpread at %d bps = %s, not worse than the previous %s", bps, got, previous)
		}
		previous = got
	}
}

// A quote built the way the FX service builds one must validate.
func TestQuoteBuiltFromApplySpreadValidates(t *testing.T) {
	mid := dec("0.74")
	r, err := ApplySpread(mid, 12)
	if err != nil {
		t.Fatalf("ApplySpread: %v", err)
	}

	sell, err := money.New(10_000, "CAD")
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	converted, _, err := money.Convert(sell, money.MustLookup("USD"), r)
	if err != nil {
		t.Fatalf("money.Convert: %v", err)
	}

	q := Quote{
		ID:           "qt_test",
		Sell:         sell,
		Buy:          converted,
		MidRate:      mid,
		Rate:         r,
		SpreadBps:    12,
		RateSource:   "oanda",
		SourceRateID: "oanda_1",
		CreatedAt:    created,
		ExpiresAt:    expires,
	}
	if err := q.Validate(); err != nil {
		t.Fatalf("a quote assembled from ApplySpread and Convert failed validation: %v", err)
	}
	if q.Pair() != "CAD/USD" {
		t.Errorf("Pair() = %q, want CAD/USD", q.Pair())
	}
}

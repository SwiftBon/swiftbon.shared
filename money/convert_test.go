package money

import (
	"errors"
	"math"
	"testing"
	"testing/quick"

	"github.com/shopspring/decimal"
)

// errAny marks a table row that must fail, where the specific sentinel is not
// the point of the case.
var errAny = errors.New("any error")

func rate(s string) Rate {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestConvert(t *testing.T) {
	tests := []struct {
		name    string
		from    Money
		to      string
		rate    string
		wantAmt int64
		wantCur string
		wantErr error
	}{
		{
			name: "exact rate leaves nothing behind",
			from: Money{10_000, "CAD"}, to: "USD", rate: "1.15",
			wantAmt: 11_500, wantCur: "USD",
		},
		{
			name: "unit rate is identity in amount",
			from: Money{12_345, "CAD"}, to: "USD", rate: "1",
			wantAmt: 12_345, wantCur: "USD",
		},
		{
			name: "rate below one",
			from: Money{10_000, "USD"}, to: "CAD", rate: "0.5",
			wantAmt: 5_000, wantCur: "CAD",
		},
		{
			name: "destination currency is normalised",
			from: Money{100, "CAD"}, to: "usd", rate: "2",
			wantAmt: 200, wantCur: "USD",
		},
		{
			// A struct literal can carry a code that never went through New.
			// Convert must reject it rather than let two spellings of one
			// currency circulate as if they were different currencies.
			name: "un-normalised source currency is rejected",
			from: Money{100, "cad"}, to: "USD", rate: "2",
			wantErr: errAny,
		},
		{
			name: "negative amount converts with its sign intact",
			from: Money{-10_000, "CAD"}, to: "USD", rate: "1.15",
			wantAmt: -11_500, wantCur: "USD",
		},
		{
			name: "zero converts to zero",
			from: Money{0, "CAD"}, to: "NGN", rate: "1000",
			wantAmt: 0, wantCur: "NGN",
		},
		{
			name: "large rate, NGN style",
			from: Money{100, "USD"}, to: "NGN", rate: "1550.75",
			wantAmt: 155_075, wantCur: "NGN",
		},
		{
			name: "same currency at rate one is a no-op",
			from: Money{999, "EUR"}, to: "EUR", rate: "1",
			wantAmt: 999, wantCur: "EUR",
		},
		{name: "unknown source currency", from: Money{100, "XYZ"}, to: "CAD", rate: "1", wantErr: ErrUnknownCurrency},
		{name: "empty source currency", from: Money{100, ""}, to: "CAD", rate: "1", wantErr: ErrEmptyCurrency},
		{name: "unknown destination currency", from: Money{100, "CAD"}, to: "XYZ", rate: "1", wantErr: ErrUnknownCurrency},
		{name: "empty destination currency", from: Money{100, "CAD"}, to: "", rate: "1", wantErr: ErrEmptyCurrency},
		{name: "zero rate", from: Money{100, "CAD"}, to: "USD", rate: "0", wantErr: ErrInvalidRate},
		{name: "negative rate", from: Money{100, "CAD"}, to: "USD", rate: "-1.5", wantErr: ErrInvalidRate},
		{
			name: "overflowing rate errors rather than wrapping",
			from: Money{MaxAmount, "CAD"}, to: "USD", rate: "2",
			wantErr: ErrOverflow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, residual, err := Convert(tt.from, Currency{Code: tt.to}, rate(tt.rate))
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("Convert returned %+v with no error, want %v", got, tt.wantErr)
				}
				if tt.wantErr != errAny && !errors.Is(err, tt.wantErr) {
					t.Fatalf("Convert error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Convert unexpected error: %v", err)
			}
			if got.Amount != tt.wantAmt {
				t.Errorf("Convert amount = %d, want %d", got.Amount, tt.wantAmt)
			}
			if got.Currency != tt.wantCur {
				t.Errorf("Convert currency = %q, want %q", got.Currency, tt.wantCur)
			}
			if residual.Currency != tt.wantCur {
				t.Errorf("residual currency = %q, want %q (residuals are in destination units)",
					residual.Currency, tt.wantCur)
			}
		})
	}
}

// The bug this package exists to prevent: int64(1.15 * 100) is 114, because
// 1.15 has no exact binary representation and the product lands at 114.999…,
// which a cast truncates. Decimal arithmetic plus explicit half-up rounding
// gives 115.
func TestConvertDoesNotTruncateLikeAFloatCast(t *testing.T) {
	// The float path that is wrong, reproduced so the contrast is on the record.
	if got := int64(1.15 * 100); got != 114 {
		t.Logf("note: int64(1.15*100) = %d on this platform", got)
	}

	src := Money{Amount: 100, Currency: "CAD"} // 1.00 CAD
	got, _, err := Convert(src, MustLookup("USD"), rate("1.15"))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Amount != 115 {
		t.Fatalf("Convert(1.00 CAD @ 1.15) = %d minor units, want 115; "+
			"a value of 114 means the truncating float path is back", got.Amount)
	}
}

// Round half-up, in both directions, at the exact boundary.
func TestConvertRoundingBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		amount  int64
		rate    string
		want    int64
		wantRes string // exact residual, in destination minor units
	}{
		{name: "exactly .5 rounds up", amount: 1, rate: "1.5", want: 2, wantRes: "-0.5"},
		{name: "exactly .5 again", amount: 3, rate: "1.5", want: 5, wantRes: "-0.5"},
		{name: "just below .5 rounds down", amount: 1, rate: "1.49", want: 1, wantRes: "0.49"},
		{name: "just above .5 rounds up", amount: 1, rate: "1.51", want: 2, wantRes: "-0.49"},
		{name: "exactly .5 negative rounds away from zero", amount: -1, rate: "1.5", want: -2, wantRes: "0.5"},
		{name: "just below .5 negative", amount: -1, rate: "1.49", want: -1, wantRes: "-0.49"},
		{name: "just above .5 negative", amount: -1, rate: "1.51", want: -2, wantRes: "0.49"},
		{name: "no rounding needed", amount: 100, rate: "2", want: 200, wantRes: "0"},
		{name: "long tail rounds down", amount: 10_000, rate: "1.23456", want: 12_346, wantRes: "-0.4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := Money{Amount: tt.amount, Currency: "CAD"}
			got, residual, err := ConvertExact(src, MustLookup("USD"), rate(tt.rate))
			if err != nil {
				t.Fatalf("ConvertExact: %v", err)
			}
			if got.Amount != tt.want {
				t.Errorf("ConvertExact(%d @ %s) = %d, want %d", tt.amount, tt.rate, got.Amount, tt.want)
			}
			if !residual.Equal(rate(tt.wantRes)) {
				t.Errorf("residual = %s, want %s", residual, tt.wantRes)
			}
		})
	}
}

// The acceptance guarantee: the residual added back reconstructs the exact
// value, so nothing has been silently discarded.
func TestConvertResidualReconstructsExactValue(t *testing.T) {
	tests := []struct {
		name   string
		amount int64
		from   string
		to     string
		rate   string
	}{
		{name: "rate with a long tail", amount: 10_000, from: "CAD", to: "USD", rate: "1.23456789"},
		{name: "rate forcing a half-up round", amount: 1, from: "CAD", to: "USD", rate: "1.5"},
		{name: "negative amount", amount: -7_777, from: "CAD", to: "USD", rate: "0.7391"},
		{name: "NGN scale", amount: 12_345, from: "USD", to: "NGN", rate: "1550.7777"},
		{name: "tiny amount", amount: 1, from: "GBP", to: "EUR", rate: "1.16666666"},
		{name: "exact rate leaves no residual", amount: 10_000, from: "CAD", to: "USD", rate: "1.15"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := Money{Amount: tt.amount, Currency: tt.from}
			dst := MustLookup(tt.to)
			r := rate(tt.rate)

			converted, residual, err := ConvertExact(src, dst, r)
			if err != nil {
				t.Fatalf("ConvertExact: %v", err)
			}

			// converted + residual must equal the exact, unrounded value.
			srcC := MustLookup(tt.from)
			exact := decimal.NewFromInt(tt.amount).Mul(r).Shift(dst.Exponent - srcC.Exponent)
			reconstructed := decimal.NewFromInt(converted.Amount).Add(residual)

			if !reconstructed.Equal(exact) {
				t.Fatalf("converted (%d) + residual (%s) = %s, want the exact value %s",
					converted.Amount, residual, reconstructed, exact)
			}
			if residual.Abs().GreaterThan(decimal.NewFromFloat(0.5)) {
				t.Errorf("residual %s exceeds half a minor unit; rounding is not nearest", residual)
			}
		})
	}
}

// A rate that forces a non-zero residual, asserted explicitly.
func TestConvertProducesNonZeroResidual(t *testing.T) {
	src := Money{Amount: 10_000, Currency: "CAD"} // 100.00 CAD
	converted, residual, err := ConvertExact(src, MustLookup("USD"), rate("1.234567"))
	if err != nil {
		t.Fatalf("ConvertExact: %v", err)
	}
	if residual.IsZero() {
		t.Fatal("expected a non-zero residual from rate 1.234567")
	}
	// 10000 × 1.234567 = 12345.67, rounds to 12346, residual −0.33.
	if converted.Amount != 12_346 {
		t.Errorf("converted = %d, want 12346", converted.Amount)
	}
	if !residual.Equal(rate("-0.33")) {
		t.Errorf("residual = %s, want -0.33", residual)
	}
}

// Residuals is the mechanism that stops fractional minor units leaking away.
// Post enough conversions and the accumulated fraction becomes a whole unit
// that the ledger can actually receive.
func TestResidualsAccumulateIntoPostableUnits(t *testing.T) {
	acc, err := NewResiduals("USD")
	if err != nil {
		t.Fatalf("NewResiduals: %v", err)
	}

	src := Money{Amount: 10_000, Currency: "CAD"}
	r := rate("1.234567") // residual −0.33 per conversion

	for i := 0; i < 10; i++ {
		_, residual, err := ConvertExact(src, MustLookup("USD"), r)
		if err != nil {
			t.Fatalf("ConvertExact: %v", err)
		}
		if err := acc.Add("USD", residual); err != nil {
			t.Fatalf("Residuals.Add: %v", err)
		}
	}

	// Ten conversions at −0.33 each is −3.3 minor units.
	if !acc.Pending().Equal(rate("-3.3")) {
		t.Fatalf("pending = %s, want -3.3", acc.Pending())
	}

	post, err := acc.Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if post.Amount != -3 || post.Currency != "USD" {
		t.Fatalf("Drain() = %+v, want {-3 USD}", post)
	}
	// The sub-unit remainder is retained, not discarded.
	if !acc.Pending().Equal(rate("-0.3")) {
		t.Fatalf("retained remainder = %s, want -0.3", acc.Pending())
	}
}

func TestResidualsRejectsWrongCurrency(t *testing.T) {
	acc, err := NewResiduals("USD")
	if err != nil {
		t.Fatalf("NewResiduals: %v", err)
	}
	if err := acc.Add("CAD", rate("0.5")); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Add(CAD) into a USD accumulator error = %v, want ErrCurrencyMismatch", err)
	}
	if _, err := NewResiduals("XYZ"); !errors.Is(err, ErrUnknownCurrency) {
		t.Fatalf("NewResiduals(XYZ) error = %v, want ErrUnknownCurrency", err)
	}
	if _, err := NewResiduals(""); !errors.Is(err, ErrEmptyCurrency) {
		t.Fatalf("NewResiduals(\"\") error = %v, want ErrEmptyCurrency", err)
	}
}

// Convert must not trust a hand-built Currency: an exponent the caller invented
// would rescale every amount by a power of ten.
func TestConvertIgnoresCallerSuppliedExponent(t *testing.T) {
	src := Money{Amount: 10_000, Currency: "CAD"}
	lying := Currency{Code: "USD", Exponent: 5, Name: "not really"}

	got, _, err := Convert(src, lying, rate("1"))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Amount != 10_000 {
		t.Fatalf("Convert with a falsified exponent = %d, want 10000; "+
			"the registry exponent must win", got.Amount)
	}
}

func TestInvert(t *testing.T) {
	got, err := Invert(rate("2"))
	if err != nil {
		t.Fatalf("Invert: %v", err)
	}
	if !got.Equal(rate("0.5")) {
		t.Errorf("Invert(2) = %s, want 0.5", got)
	}
	if _, err := Invert(rate("0")); !errors.Is(err, ErrInvalidRate) {
		t.Errorf("Invert(0) error = %v, want ErrInvalidRate", err)
	}
	if _, err := Invert(rate("-1")); !errors.Is(err, ErrInvalidRate) {
		t.Errorf("Invert(-1) error = %v, want ErrInvalidRate", err)
	}
}

// Converting and converting back at the inverse rate must land within the
// rounding tolerance.
//
// The naive statement of this property — "within one minor unit" — is only true
// when the rate does not compress magnitude. Rounding the destination amount to
// a whole minor unit costs at most half a destination minor unit, which is
// 0.5/rate source minor units; converting back costs another half. So the true
// bound is 0.5/rate + 0.5, and at a rate of 0.00065 a single destination minor
// unit is worth over 1500 source ones — converting ₦1.00 to USD and back really
// does return zero, because a hundredth of a US cent cannot be held.
//
// That loss is not a defect in Convert; it is the reason the residual exists.
// Asserting the honest bound here, rather than a bound that happens to hold for
// the rates we quote today, is what keeps this test meaningful when the desk
// starts quoting a pair where it does not.
func roundTripTolerance(r Rate) int64 {
	half := decimal.NewFromFloat(0.5)
	bound := half.Div(r).Add(half).Ceil()
	if !bound.BigInt().IsInt64() {
		return math.MaxInt64
	}
	return bound.IntPart()
}

func TestConvertRoundTripsWithinTolerance(t *testing.T) {
	rates := []string{"1.15", "1.234567", "0.7391", "1550.7777", "0.00065", "1", "3.14159265"}
	amounts := []int64{1, 7, 100, 9_999, 1_000_000, -1, -12_345, 0}

	for _, rs := range rates {
		for _, amt := range amounts {
			r := rate(rs)
			inv, err := Invert(r)
			if err != nil {
				t.Fatalf("Invert(%s): %v", rs, err)
			}

			src := Money{Amount: amt, Currency: "CAD"}
			mid, _, err := Convert(src, MustLookup("USD"), r)
			if err != nil {
				t.Fatalf("Convert(%d @ %s): %v", amt, rs, err)
			}
			back, _, err := Convert(mid, MustLookup("CAD"), inv)
			if err != nil {
				t.Fatalf("Convert back(%d @ %s): %v", mid.Amount, inv, err)
			}

			tol := roundTripTolerance(r)
			diff := back.Amount - src.Amount
			if diff < -tol || diff > tol {
				t.Errorf("round trip of %d CAD at rate %s returned %d, drift %d minor units (tolerance %d)",
					amt, rs, back.Amount, diff, tol)
			}
			if back.Currency != "CAD" {
				t.Errorf("round trip currency = %q, want CAD", back.Currency)
			}
		}
	}
}

// For any rate at or above 1 the destination unit is at least as fine as the
// source unit, so the tolerance collapses to the one minor unit that is the
// intuitive guarantee. This is the case every launch pair falls into.
func TestConvertRoundTripsWithinOneMinorUnitWhenRateAtLeastOne(t *testing.T) {
	rates := []string{"1", "1.15", "1.234567", "3.14159265", "1550.7777"}
	amounts := []int64{0, 1, 7, 100, 9_999, 1_000_000, -1, -12_345}

	for _, rs := range rates {
		for _, amt := range amounts {
			r := rate(rs)
			inv, err := Invert(r)
			if err != nil {
				t.Fatalf("Invert(%s): %v", rs, err)
			}
			src := Money{Amount: amt, Currency: "CAD"}
			mid, _, err := Convert(src, MustLookup("USD"), r)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			back, _, err := Convert(mid, MustLookup("CAD"), inv)
			if err != nil {
				t.Fatalf("Convert back: %v", err)
			}
			if diff := back.Amount - src.Amount; diff < -1 || diff > 1 {
				t.Errorf("round trip of %d CAD at rate %s returned %d, drift %d (want |drift| <= 1)",
					amt, rs, back.Amount, diff)
			}
		}
	}
}

func TestPropertyConvertRoundTripsWithinTolerance(t *testing.T) {
	f := func(amount int32, rateNum uint16) bool {
		if rateNum == 0 {
			return true
		}
		r := decimal.NewFromInt(int64(rateNum)).Div(decimal.NewFromInt(1000))
		inv, err := Invert(r)
		if err != nil {
			return false
		}
		src := Money{Amount: int64(amount), Currency: "CAD"}
		mid, _, err := Convert(src, MustLookup("USD"), r)
		if err != nil {
			return false
		}
		back, _, err := Convert(mid, MustLookup("CAD"), inv)
		if err != nil {
			return false
		}
		tol := roundTripTolerance(r)
		diff := back.Amount - src.Amount
		return diff >= -tol && diff <= tol
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("round trip drifted beyond the rounding tolerance: %v", err)
	}
}

func TestPropertyConvertPreservesSign(t *testing.T) {
	f := func(amount int32) bool {
		src := Money{Amount: int64(amount) * 100, Currency: "CAD"}
		got, _, err := Convert(src, MustLookup("USD"), rate("1.15"))
		if err != nil {
			return false
		}
		switch {
		case src.IsZero():
			return got.IsZero()
		case src.IsNegative():
			return got.IsNegative()
		default:
			return got.IsPositive()
		}
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("Convert did not preserve sign: %v", err)
	}
}

func TestRoundHalfUp(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0.5", "1"},
		{"1.5", "2"},
		{"2.5", "3"}, // half-up, not banker's rounding
		{"-0.5", "-1"},
		{"-1.5", "-2"},
		{"-2.5", "-3"},
		{"0.49", "0"},
		{"0.51", "1"},
		{"-0.49", "0"},
		{"-0.51", "-1"},
		{"7", "7"},
	}
	for _, tt := range tests {
		if got := roundHalfUp(rate(tt.in)); !got.Equal(rate(tt.want)) {
			t.Errorf("roundHalfUp(%s) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestConvertRejectsInvalidSourceMoney(t *testing.T) {
	// An un-normalised code on a struct literal must not slip through.
	if _, _, err := Convert(Money{100, "cad"}, MustLookup("USD"), rate("1")); err == nil {
		t.Error("Convert accepted a Money with an un-normalised currency")
	}
}

// The guards on the extremes of the representable range. These are the branches
// that decide whether a pathological conversion fails loudly or produces a
// number with the wrong sign.
func TestConvertRejectsResultsAtTheEdgeOfTheRange(t *testing.T) {
	// -4611686018427387904 × 2 is exactly math.MinInt64, the one int64 value
	// Money refuses to hold because its negation does not fit.
	src := Money{Amount: -4_611_686_018_427_387_904, Currency: "CAD"}
	if _, _, err := ConvertExact(src, MustLookup("USD"), rate("2")); !errors.Is(err, ErrAmountOutOfRange) {
		t.Fatalf("converting to exactly MinInt64 error = %v, want ErrAmountOutOfRange", err)
	}

	// And the same through Convert, so both entry points are guarded.
	if _, _, err := Convert(src, MustLookup("USD"), rate("2")); !errors.Is(err, ErrAmountOutOfRange) {
		t.Fatalf("Convert to exactly MinInt64 error = %v, want ErrAmountOutOfRange", err)
	}

	// A result one below the minimum overflows instead.
	if _, _, err := Convert(Money{MinAmount, "CAD"}, MustLookup("USD"), rate("2")); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Convert past MinInt64 error = %v, want ErrOverflow", err)
	}
}

// Convert's Money residual is always zero for a single conversion, because the
// discarded value is by construction smaller than one minor unit. Asserting it
// keeps anyone from reading a zero here as "nothing was lost" — ConvertExact is
// where the loss is visible.
func TestConvertMoneyResidualIsAlwaysZeroForOneConversion(t *testing.T) {
	for _, rs := range []string{"1.234567", "1.5", "0.7391", "1550.7777", "1.15"} {
		for _, amt := range []int64{1, -1, 7, 10_000, -12_345} {
			_, residual, err := Convert(Money{amt, "CAD"}, MustLookup("USD"), rate(rs))
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if !residual.IsZero() {
				t.Errorf("Convert(%d @ %s) residual = %s, want zero; a single "+
					"conversion cannot discard a whole minor unit", amt, rs, residual)
			}
			if residual.Currency != "USD" {
				t.Errorf("residual currency = %q, want USD", residual.Currency)
			}
		}
	}
}

func TestResidualsDrainRejectsOutOfRangeTotals(t *testing.T) {
	acc, err := NewResiduals("USD")
	if err != nil {
		t.Fatalf("NewResiduals: %v", err)
	}
	// A total far beyond int64. Drain must refuse rather than truncate to a
	// number the ledger would accept.
	if err := acc.Add("USD", rate("99999999999999999999999")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := acc.Drain(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Drain of an out-of-range total error = %v, want ErrOverflow", err)
	}

	// Exactly math.MinInt64 is in int64 range but outside Money's range.
	edge, err := NewResiduals("USD")
	if err != nil {
		t.Fatalf("NewResiduals: %v", err)
	}
	if err := edge.Add("USD", rate("-9223372036854775808")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := edge.Drain(); !errors.Is(err, ErrAmountOutOfRange) {
		t.Fatalf("Drain of a MinInt64 total error = %v, want ErrAmountOutOfRange", err)
	}
}

func TestResidualsDrainWithNothingPending(t *testing.T) {
	acc, err := NewResiduals("CAD")
	if err != nil {
		t.Fatalf("NewResiduals: %v", err)
	}
	got, err := acc.Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if !got.IsZero() || got.Currency != "CAD" {
		t.Fatalf("Drain() on an empty accumulator = %+v, want {0 CAD}", got)
	}
}

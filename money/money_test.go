package money

import (
	"errors"
	"math"
	"testing"
	"testing/quick"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		amount   int64
		currency string
		wantAmt  int64
		wantCur  string
		wantErr  error
	}{
		{name: "positive CAD", amount: 12345, currency: "CAD", wantAmt: 12345, wantCur: "CAD"},
		{name: "negative CAD", amount: -12345, currency: "CAD", wantAmt: -12345, wantCur: "CAD"},
		{name: "zero", amount: 0, currency: "USD", wantAmt: 0, wantCur: "USD"},
		{name: "lowercase currency is normalised", amount: 100, currency: "ngn", wantAmt: 100, wantCur: "NGN"},
		{name: "padded currency is trimmed", amount: 100, currency: " eur ", wantAmt: 100, wantCur: "EUR"},
		{name: "max amount", amount: MaxAmount, currency: "GBP", wantAmt: MaxAmount, wantCur: "GBP"},
		{name: "min amount", amount: MinAmount, currency: "GBP", wantAmt: MinAmount, wantCur: "GBP"},
		{name: "empty currency", amount: 100, currency: "", wantErr: ErrEmptyCurrency},
		{name: "unknown currency", amount: 100, currency: "XYZ", wantErr: ErrUnknownCurrency},
		{name: "MinInt64 is outside the representable range", amount: math.MinInt64, currency: "CAD", wantErr: ErrAmountOutOfRange},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(tt.amount, tt.currency)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("New(%d, %q) error = %v, want %v", tt.amount, tt.currency, err, tt.wantErr)
				}
				if got != (Money{}) {
					t.Errorf("New returned %+v alongside an error; must return the zero Money", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%d, %q) unexpected error: %v", tt.amount, tt.currency, err)
			}
			if got.Amount != tt.wantAmt || got.Currency != tt.wantCur {
				t.Errorf("New(%d, %q) = %+v, want {%d %s}", tt.amount, tt.currency, got, tt.wantAmt, tt.wantCur)
			}
		})
	}
}

func TestZero(t *testing.T) {
	tests := []struct{ in, wantCur string }{
		{"CAD", "CAD"},
		{"cad", "CAD"},
		{" usd ", "USD"},
	}
	for _, tt := range tests {
		got := Zero(tt.in)
		if got.Amount != 0 {
			t.Errorf("Zero(%q).Amount = %d, want 0", tt.in, got.Amount)
		}
		if got.Currency != tt.wantCur {
			t.Errorf("Zero(%q).Currency = %q, want %q", tt.in, got.Currency, tt.wantCur)
		}
		if !got.IsZero() {
			t.Errorf("Zero(%q).IsZero() = false", tt.in)
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		m       Money
		wantErr error
	}{
		{name: "valid", m: Money{Amount: 100, Currency: "CAD"}},
		{name: "valid negative", m: Money{Amount: -100, Currency: "CAD"}},
		{name: "valid zero", m: Money{Amount: 0, Currency: "EUR"}},
		{name: "empty currency", m: Money{Amount: 100, Currency: ""}, wantErr: ErrEmptyCurrency},
		{name: "unknown currency", m: Money{Amount: 100, Currency: "XYZ"}, wantErr: ErrUnknownCurrency},
		{name: "MinInt64 amount", m: Money{Amount: math.MinInt64, Currency: "CAD"}, wantErr: ErrAmountOutOfRange},
		{name: "zero value Money", m: Money{}, wantErr: ErrEmptyCurrency},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.m.Validate()
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

// A struct literal can carry an un-normalised code that never went through New.
// Validate must reject it rather than let two spellings of one currency coexist.
func TestValidateRejectsUnnormalisedCurrency(t *testing.T) {
	m := Money{Amount: 100, Currency: "cad"}
	if err := m.Validate(); err == nil {
		t.Fatal("Validate() accepted a lowercase currency on a struct literal")
	}
}

func TestAdd(t *testing.T) {
	tests := []struct {
		name    string
		a, b    Money
		want    Money
		wantErr error
	}{
		{
			name: "same currency",
			a:    Money{1000, "CAD"}, b: Money{500, "CAD"},
			want: Money{1500, "CAD"},
		},
		{
			name: "adding a negative",
			a:    Money{1000, "CAD"}, b: Money{-500, "CAD"},
			want: Money{500, "CAD"},
		},
		{
			name: "crossing zero",
			a:    Money{100, "USD"}, b: Money{-300, "USD"},
			want: Money{-200, "USD"},
		},
		{
			name: "adding zero is identity",
			a:    Money{12345, "NGN"}, b: Money{0, "NGN"},
			want: Money{12345, "NGN"},
		},
		{
			name: "CAD plus USD must not coerce",
			a:    Money{1000, "CAD"}, b: Money{500, "USD"},
			wantErr: ErrCurrencyMismatch,
		},
		{
			name: "NGN plus GBP must not coerce",
			a:    Money{1, "NGN"}, b: Money{1, "GBP"},
			wantErr: ErrCurrencyMismatch,
		},
		{
			name: "positive overflow errors rather than wrapping",
			a:    Money{MaxAmount, "CAD"}, b: Money{1, "CAD"},
			wantErr: ErrOverflow,
		},
		{
			name: "large positive overflow",
			a:    Money{MaxAmount - 10, "CAD"}, b: Money{100, "CAD"},
			wantErr: ErrOverflow,
		},
		{
			name: "negative overflow errors rather than wrapping",
			a:    Money{MinAmount, "CAD"}, b: Money{-1, "CAD"},
			wantErr: ErrOverflow,
		},
		{
			name: "adding to the maximum is fine when it fits",
			a:    Money{MaxAmount - 1, "CAD"}, b: Money{1, "CAD"},
			want: Money{MaxAmount, "CAD"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.a.Add(tt.b)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("%s.Add(%s) error = %v, want %v", tt.a, tt.b, err, tt.wantErr)
				}
				if got != (Money{}) {
					t.Errorf("Add returned %+v alongside an error; must return the zero Money", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s.Add(%s) unexpected error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Errorf("%s.Add(%s) = %+v, want %+v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// The guard this whole package exists for: a CAD amount must never be silently
// absorbed into a USD balance.
func TestAddMismatchedCurrenciesReturnsError(t *testing.T) {
	cad := Money{Amount: 10_000, Currency: "CAD"}
	usd := Money{Amount: 10_000, Currency: "USD"}

	got, err := cad.Add(usd)
	if err == nil {
		t.Fatalf("CAD.Add(USD) returned %+v with no error; mismatched currencies must never be coerced", got)
	}
	if !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("CAD.Add(USD) error = %v, want ErrCurrencyMismatch", err)
	}
	if got != (Money{}) {
		t.Fatalf("CAD.Add(USD) returned %+v; must return the zero Money on error", got)
	}
	// And the other direction, so the guard is not one-sided.
	if _, err := usd.Add(cad); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("USD.Add(CAD) error = %v, want ErrCurrencyMismatch", err)
	}
}

func TestSub(t *testing.T) {
	tests := []struct {
		name    string
		a, b    Money
		want    Money
		wantErr error
	}{
		{name: "same currency", a: Money{1000, "CAD"}, b: Money{300, "CAD"}, want: Money{700, "CAD"}},
		{name: "result goes negative", a: Money{100, "CAD"}, b: Money{300, "CAD"}, want: Money{-200, "CAD"}},
		{name: "subtracting a negative adds", a: Money{100, "USD"}, b: Money{-300, "USD"}, want: Money{400, "USD"}},
		{name: "subtracting zero is identity", a: Money{999, "EUR"}, b: Money{0, "EUR"}, want: Money{999, "EUR"}},
		{name: "currency mismatch", a: Money{1000, "CAD"}, b: Money{300, "USD"}, wantErr: ErrCurrencyMismatch},
		{name: "negative overflow", a: Money{MinAmount, "CAD"}, b: Money{1, "CAD"}, wantErr: ErrOverflow},
		{name: "positive overflow via negative operand", a: Money{MaxAmount, "CAD"}, b: Money{-1, "CAD"}, wantErr: ErrOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.a.Sub(tt.b)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("%s.Sub(%s) error = %v, want %v", tt.a, tt.b, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s.Sub(%s) unexpected error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Errorf("%s.Sub(%s) = %+v, want %+v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestNeg(t *testing.T) {
	tests := []struct{ in, want Money }{
		{Money{100, "CAD"}, Money{-100, "CAD"}},
		{Money{-100, "CAD"}, Money{100, "CAD"}},
		{Money{0, "USD"}, Money{0, "USD"}},
		{Money{MaxAmount, "GBP"}, Money{MinAmount, "GBP"}},
		{Money{MinAmount, "GBP"}, Money{MaxAmount, "GBP"}},
	}
	for _, tt := range tests {
		if got := tt.in.Neg(); got != tt.want {
			t.Errorf("%s.Neg() = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

// The representable range is symmetric precisely so that negation is total.
func TestNegIsAlwaysExact(t *testing.T) {
	f := func(amount int64) bool {
		if amount == math.MinInt64 {
			return true // outside the representable range by construction
		}
		m := Money{Amount: amount, Currency: "CAD"}
		return m.Neg().Neg() == m
	}
	if err := quick.Check(f, nil); err != nil {
		t.Errorf("Neg is not an involution: %v", err)
	}
}

func TestIsZeroIsNegativeIsPositive(t *testing.T) {
	tests := []struct {
		m                        Money
		zero, negative, positive bool
	}{
		{Money{0, "CAD"}, true, false, false},
		{Money{1, "CAD"}, false, false, true},
		{Money{-1, "CAD"}, false, true, false},
		{Money{MaxAmount, "CAD"}, false, false, true},
		{Money{MinAmount, "CAD"}, false, true, false},
	}
	for _, tt := range tests {
		if got := tt.m.IsZero(); got != tt.zero {
			t.Errorf("%s.IsZero() = %v, want %v", tt.m, got, tt.zero)
		}
		if got := tt.m.IsNegative(); got != tt.negative {
			t.Errorf("%s.IsNegative() = %v, want %v", tt.m, got, tt.negative)
		}
		if got := tt.m.IsPositive(); got != tt.positive {
			t.Errorf("%s.IsPositive() = %v, want %v", tt.m, got, tt.positive)
		}
	}
}

func TestCmp(t *testing.T) {
	tests := []struct {
		name    string
		a, b    Money
		want    int
		wantErr error
	}{
		{name: "less", a: Money{100, "CAD"}, b: Money{200, "CAD"}, want: -1},
		{name: "greater", a: Money{200, "CAD"}, b: Money{100, "CAD"}, want: 1},
		{name: "equal", a: Money{100, "CAD"}, b: Money{100, "CAD"}, want: 0},
		{name: "negative less than zero", a: Money{-1, "USD"}, b: Money{0, "USD"}, want: -1},
		{name: "extremes", a: Money{MinAmount, "USD"}, b: Money{MaxAmount, "USD"}, want: -1},
		{name: "normalisation does not affect equality", a: Money{100, "CAD"}, b: Money{100, "cad"}, want: 0},
		{name: "currency mismatch", a: Money{100, "CAD"}, b: Money{100, "USD"}, wantErr: ErrCurrencyMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.a.Cmp(tt.b)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Cmp error = %v, want %v", err, tt.wantErr)
				}
				if got != 0 {
					t.Errorf("Cmp returned %d alongside an error, want 0", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Cmp unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("%s.Cmp(%s) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// Cmp must refuse to order two different currencies. Returning any number here
// would let a balance check pass against the wrong denomination.
func TestCmpMismatchedCurrenciesReturnsError(t *testing.T) {
	if _, err := (Money{1, "CAD"}).Cmp(Money{1_000_000, "NGN"}); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Cmp across currencies error = %v, want ErrCurrencyMismatch", err)
	}
}

// --- Property tests for the arithmetic invariants ---

// randomMoney builds a Money whose amount is well inside the representable
// range, so that a property failure means the invariant broke rather than that
// the test manufactured an overflow.
func randomMoney(amount int64, currency string) Money {
	return Money{Amount: amount % (math.MaxInt64 / 4), Currency: currency}
}

func TestPropertyAddIsCommutative(t *testing.T) {
	f := func(a, b int64) bool {
		x, y := randomMoney(a, "CAD"), randomMoney(b, "CAD")
		left, errL := x.Add(y)
		right, errR := y.Add(x)
		if (errL == nil) != (errR == nil) {
			return false
		}
		if errL != nil {
			return true // both overflowed; commutativity holds vacuously
		}
		return left == right
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("Add is not commutative: %v", err)
	}
}

func TestPropertyAddIsAssociative(t *testing.T) {
	f := func(a, b, c int64) bool {
		x, y, z := randomMoney(a, "USD"), randomMoney(b, "USD"), randomMoney(c, "USD")
		ab, err1 := x.Add(y)
		if err1 != nil {
			return true
		}
		left, err2 := ab.Add(z)
		bc, err3 := y.Add(z)
		if err3 != nil {
			return true
		}
		right, err4 := x.Add(bc)
		if (err2 == nil) != (err4 == nil) {
			return true // one association order overflowed; not a violation
		}
		if err2 != nil {
			return true
		}
		return left == right
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("Add is not associative: %v", err)
	}
}

func TestPropertyAddThenSubIsIdentity(t *testing.T) {
	f := func(a, b int64) bool {
		m, n := randomMoney(a, "NGN"), randomMoney(b, "NGN")
		sum, err := m.Add(n)
		if err != nil {
			return true
		}
		back, err := sum.Sub(n)
		if err != nil {
			return false
		}
		return back == m
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("m.Add(n).Sub(n) != m: %v", err)
	}
}

func TestPropertyZeroIsTheAdditiveIdentity(t *testing.T) {
	f := func(a int64) bool {
		m := randomMoney(a, "EUR")
		got, err := m.Add(Zero("EUR"))
		return err == nil && got == m
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("adding zero is not the identity: %v", err)
	}
}

func TestPropertySubIsAddNeg(t *testing.T) {
	f := func(a, b int64) bool {
		m, n := randomMoney(a, "GBP"), randomMoney(b, "GBP")
		viaSub, errS := m.Sub(n)
		viaAdd, errA := m.Add(n.Neg())
		if (errS == nil) != (errA == nil) {
			return false
		}
		if errS != nil {
			return true
		}
		return viaSub == viaAdd
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("Sub disagrees with Add(Neg): %v", err)
	}
}

func TestPropertyCmpIsAntisymmetric(t *testing.T) {
	f := func(a, b int64) bool {
		m, n := randomMoney(a, "CAD"), randomMoney(b, "CAD")
		fwd, err1 := m.Cmp(n)
		rev, err2 := n.Cmp(m)
		return err1 == nil && err2 == nil && fwd == -rev
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("Cmp is not antisymmetric: %v", err)
	}
}

// Every currency pairing that differs must error, on every binary operation.
// This is the estate-wide guard, asserted exhaustively rather than by example.
func TestEveryBinaryOpRejectsEveryMismatchedPair(t *testing.T) {
	codes := []string{"CAD", "USD", "NGN", "GBP", "EUR"}
	for _, a := range codes {
		for _, b := range codes {
			if a == b {
				continue
			}
			x, y := Money{100, a}, Money{100, b}
			t.Run(a+"_"+b, func(t *testing.T) {
				if _, err := x.Add(y); !errors.Is(err, ErrCurrencyMismatch) {
					t.Errorf("Add(%s,%s) error = %v, want ErrCurrencyMismatch", a, b, err)
				}
				if _, err := x.Sub(y); !errors.Is(err, ErrCurrencyMismatch) {
					t.Errorf("Sub(%s,%s) error = %v, want ErrCurrencyMismatch", a, b, err)
				}
				if _, err := x.Cmp(y); !errors.Is(err, ErrCurrencyMismatch) {
					t.Errorf("Cmp(%s,%s) error = %v, want ErrCurrencyMismatch", a, b, err)
				}
			})
		}
	}
}

// Overflow must never wrap. A wrapped balance is a balance with the wrong sign,
// which is worse than a failed request.
func TestOverflowNeverWraps(t *testing.T) {
	if _, err := (Money{MaxAmount, "CAD"}).Add(Money{MaxAmount, "CAD"}); !errors.Is(err, ErrOverflow) {
		t.Errorf("max+max error = %v, want ErrOverflow", err)
	}
	if _, err := (Money{MinAmount, "CAD"}).Add(Money{MinAmount, "CAD"}); !errors.Is(err, ErrOverflow) {
		t.Errorf("min+min error = %v, want ErrOverflow", err)
	}
	if _, err := (Money{MinAmount, "CAD"}).Sub(Money{MaxAmount, "CAD"}); !errors.Is(err, ErrOverflow) {
		t.Errorf("min-max error = %v, want ErrOverflow", err)
	}
	if _, err := (Money{MaxAmount, "CAD"}).Sub(Money{MinAmount, "CAD"}); !errors.Is(err, ErrOverflow) {
		t.Errorf("max-min error = %v, want ErrOverflow", err)
	}
}

// A property sweep over the whole range: no Add or Sub may ever produce a
// result whose sign contradicts the operands, which is the observable symptom
// of a wrap.
func TestPropertyArithmeticNeverWraps(t *testing.T) {
	f := func(a, b int64) bool {
		if a == math.MinInt64 || b == math.MinInt64 {
			return true
		}
		m, n := Money{a, "CAD"}, Money{b, "CAD"}
		if sum, err := m.Add(n); err == nil {
			if a > 0 && b > 0 && sum.Amount < a {
				return false
			}
			if a < 0 && b < 0 && sum.Amount > a {
				return false
			}
		}
		if diff, err := m.Sub(n); err == nil {
			if b > 0 && diff.Amount > a {
				return false
			}
			if b < 0 && diff.Amount < a {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 2000}); err != nil {
		t.Errorf("arithmetic wrapped: %v", err)
	}
}

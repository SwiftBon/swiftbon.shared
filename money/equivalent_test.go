package money_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"swiftbon.com/shared/money"
)

func TestEquivalent(t *testing.T) {
	d := func(s string) decimal.Decimal { return decimal.RequireFromString(s) }
	tests := []struct {
		name   string
		in     money.Money
		target string
		rate   decimal.Decimal
		want   money.Money
	}{
		{"NGN to USD", money.Money{Amount: 5_000_000_00, Currency: "NGN"}, "USD", d("0.00064516"), money.Money{Amount: 322_580, Currency: "USD"}},
		{"USD to NGN", money.Money{Amount: 10_000_00, Currency: "USD"}, "NGN", d("1550"), money.Money{Amount: 1_550_000_000, Currency: "NGN"}},
		{"rounds half up", money.Money{Amount: 1, Currency: "USD"}, "GBP", d("0.5"), money.Money{Amount: 1, Currency: "GBP"}},
		{"negative rounds away from zero", money.Money{Amount: -1, Currency: "USD"}, "GBP", d("0.5"), money.Money{Amount: -1, Currency: "GBP"}},
		{"same currency ignores rate", money.Money{Amount: 123, Currency: "USD"}, "usd", d("99"), money.Money{Amount: 123, Currency: "USD"}},
		{"lower-case target normalised", money.Money{Amount: 100, Currency: "USD"}, "eur", d("0.9"), money.Money{Amount: 90, Currency: "EUR"}},
	}
	for _, tc := range tests {
		got, err := money.Equivalent(tc.in, tc.target, tc.rate)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %v, %v want %v", tc.name, got, err, tc.want)
		}
	}
}

func TestEquivalentErrors(t *testing.T) {
	usd := money.Money{Amount: 100, Currency: "USD"}
	if _, err := money.Equivalent(usd, "XXX", decimal.NewFromInt(1)); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Errorf("unknown target: %v", err)
	}
	if _, err := money.Equivalent(money.Money{Amount: 1, Currency: "ZZZ"}, "USD", decimal.NewFromInt(1)); err == nil {
		t.Error("unknown source accepted")
	}
	if _, err := money.Equivalent(money.Money{Amount: -1 << 63, Currency: "USD"}, "USD", decimal.NewFromInt(1)); err == nil {
		t.Error("out of range accepted")
	}
	if _, err := money.Equivalent(usd, "NGN", decimal.Zero); !errors.Is(err, money.ErrInvalidRate) {
		t.Errorf("zero rate: %v", err)
	}
	if _, err := money.Equivalent(usd, "NGN", decimal.NewFromInt(-5)); !errors.Is(err, money.ErrInvalidRate) {
		t.Errorf("negative rate: %v", err)
	}
}

func TestParseMinor(t *testing.T) {
	good := map[string]int64{"0": 0, "12345": 12345, "-250": -250, "007": 7, "9223372036854775807": 1<<63 - 1}
	for s, want := range good {
		got, err := money.ParseMinor(s, "ngn")
		if err != nil || got.Amount != want || got.Currency != "NGN" {
			t.Errorf("ParseMinor(%q) = %v, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "-", "12.50", "1e3", "+5", " 5", "5 ", "1,000", "abc", "--5", "0x10", "١٢"} {
		if _, err := money.ParseMinor(s, "NGN"); !errors.Is(err, money.ErrInvalidMinorUnits) {
			t.Errorf("ParseMinor(%q) err = %v", s, err)
		}
	}
	if _, err := money.ParseMinor("9223372036854775808", "NGN"); !errors.Is(err, money.ErrOverflow) {
		t.Errorf("overflow: %v", err)
	}
	if _, err := money.ParseMinor("-9223372036854775808", "NGN"); !errors.Is(err, money.ErrAmountOutOfRange) {
		t.Errorf("min int64: %v", err)
	}
	if _, err := money.ParseMinor("5", "ZZZ"); err == nil {
		t.Error("unknown currency accepted")
	}
}

func TestParseMinorRoundTrip(t *testing.T) {
	for _, n := range []int64{0, 1, -1, 99, 123456789, -123456789} {
		m := money.Money{Amount: n, Currency: "USD"}
		got, err := money.ParseMinor(money.Format(n, 0), "USD")
		if err != nil || got != m {
			t.Errorf("round trip %d: %v %v", n, got, err)
		}
	}
}

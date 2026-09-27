package money

import (
	"fmt"
	"sort"
	"strings"
)

// Currency is an ISO 4217 currency as Swiftbon models it.
//
// Exponent is the number of decimal places in the currency's minor unit: it is
// the bridge between the int64 minor-unit amounts this package stores and the
// major-unit values people read. Every launch currency has an exponent of 2, so
// the field is currently constant — it is carried anyway because the day JPY
// (exponent 0) or KWD (exponent 3) is added, code that ignored the exponent
// silently misstates every amount by two orders of magnitude.
type Currency struct {
	Code     string // ISO 4217 alpha-3, uppercase
	Exponent int32  // decimal places in the minor unit
	Name     string
}

// registry is the single source of truth for what Swiftbon can hold and move.
// Adding a currency here is the only supported way to make it usable.
var registry = map[string]Currency{
	"CAD": {Code: "CAD", Exponent: 2, Name: "Canadian Dollar"},
	"USD": {Code: "USD", Exponent: 2, Name: "United States Dollar"},
	"NGN": {Code: "NGN", Exponent: 2, Name: "Nigerian Naira"},
	"GBP": {Code: "GBP", Exponent: 2, Name: "Pound Sterling"},
	"EUR": {Code: "EUR", Exponent: 2, Name: "Euro"},
}

// Normalise canonicalises a currency code for lookup and storage: whitespace
// trimmed, uppercased. It does not validate — use Lookup for that.
func Normalise(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// Lookup resolves a currency code to its definition.
//
// An empty or unrecognised code is an error and is never defaulted. A payment
// whose currency the caller failed to supply is a payment nobody has decided the
// meaning of; substituting a house currency turns that omission into a silently
// mispriced ledger entry.
func Lookup(code string) (Currency, error) {
	norm := Normalise(code)
	if norm == "" {
		return Currency{}, ErrEmptyCurrency
	}
	c, ok := registry[norm]
	if !ok {
		return Currency{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, code)
	}
	return c, nil
}

// IsSupported reports whether code names a currency in the registry.
func IsSupported(code string) bool {
	_, err := Lookup(code)
	return err == nil
}

// Supported returns every registered currency, sorted by Code, for API
// discovery endpoints. The slice is freshly built on each call, so callers may
// retain and mutate it without corrupting the registry.
func Supported() []Currency {
	out := make([]Currency, 0, len(registry))
	for _, c := range registry {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// MustLookup is Lookup for contexts where an unknown code is a programming
// error rather than bad input: package-level vars, test fixtures, config
// validated at boot. It panics. Never call it on a value derived from a request.
func MustLookup(code string) Currency {
	c, err := Lookup(code)
	if err != nil {
		panic(err)
	}
	return c
}

package money

import (
	"errors"
	"testing"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		wantCode string
		wantExp  int32
		wantErr  error
	}{
		{name: "canonical CAD", code: "CAD", wantCode: "CAD", wantExp: 2},
		{name: "canonical USD", code: "USD", wantCode: "USD", wantExp: 2},
		{name: "canonical NGN", code: "NGN", wantCode: "NGN", wantExp: 2},
		{name: "canonical GBP", code: "GBP", wantCode: "GBP", wantExp: 2},
		{name: "canonical EUR", code: "EUR", wantCode: "EUR", wantExp: 2},
		{name: "lowercase is normalised", code: "cad", wantCode: "CAD", wantExp: 2},
		{name: "mixed case is normalised", code: "cAd", wantCode: "CAD", wantExp: 2},
		{name: "surrounding whitespace is trimmed", code: "  usd  ", wantCode: "USD", wantExp: 2},
		{name: "empty is an error, never a default", code: "", wantErr: ErrEmptyCurrency},
		{name: "whitespace only is empty", code: "   ", wantErr: ErrEmptyCurrency},
		{name: "unknown code is an error", code: "XYZ", wantErr: ErrUnknownCurrency},
		{name: "unlisted real currency is still an error", code: "JPY", wantErr: ErrUnknownCurrency},
		{name: "wrong length is an error", code: "CADD", wantErr: ErrUnknownCurrency},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Lookup(tt.code)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Lookup(%q) error = %v, want %v", tt.code, err, tt.wantErr)
				}
				if got != (Currency{}) {
					t.Errorf("Lookup(%q) returned %+v alongside an error; must return the zero Currency", tt.code, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Lookup(%q) unexpected error: %v", tt.code, err)
			}
			if got.Code != tt.wantCode {
				t.Errorf("Lookup(%q).Code = %q, want %q", tt.code, got.Code, tt.wantCode)
			}
			if got.Exponent != tt.wantExp {
				t.Errorf("Lookup(%q).Exponent = %d, want %d", tt.code, got.Exponent, tt.wantExp)
			}
			if got.Name == "" {
				t.Errorf("Lookup(%q).Name is empty", tt.code)
			}
		})
	}
}

func TestIsSupported(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{"CAD", true},
		{"cad", true},
		{"USD", true},
		{"NGN", true},
		{"GBP", true},
		{"EUR", true},
		{"", false},
		{"XYZ", false},
		{"JPY", false},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			if got := IsSupported(tt.code); got != tt.want {
				t.Errorf("IsSupported(%q) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}

func TestSupported(t *testing.T) {
	got := Supported()

	want := []string{"CAD", "EUR", "GBP", "NGN", "USD"}
	if len(got) != len(want) {
		t.Fatalf("Supported() returned %d currencies, want %d: %+v", len(got), len(want), got)
	}
	for i, c := range got {
		if c.Code != want[i] {
			t.Errorf("Supported()[%d].Code = %q, want %q (result must be sorted by Code)", i, c.Code, want[i])
		}
		if c.Exponent != 2 {
			t.Errorf("Supported()[%d] %s has exponent %d, want 2", i, c.Code, c.Exponent)
		}
	}
}

// Supported hands out a fresh slice each call; a caller mutating the result
// must not be able to corrupt the registry for everyone else in the process.
func TestSupportedIsACopy(t *testing.T) {
	first := Supported()
	first[0] = Currency{Code: "ZZZ", Exponent: 9, Name: "tampered"}

	second := Supported()
	if second[0].Code != "CAD" {
		t.Fatalf("mutating the result of Supported() corrupted the registry: got %+v", second[0])
	}
	if c := MustLookup("CAD"); c.Exponent != 2 {
		t.Fatalf("registry CAD exponent = %d after tampering, want 2", c.Exponent)
	}
}

func TestMustLookup(t *testing.T) {
	if got := MustLookup("cad"); got.Code != "CAD" {
		t.Errorf("MustLookup(%q).Code = %q, want CAD", "cad", got.Code)
	}

	t.Run("panics on unknown", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Error("MustLookup(\"XYZ\") did not panic")
			}
		}()
		MustLookup("XYZ")
	})

	t.Run("panics on empty", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Error("MustLookup(\"\") did not panic")
			}
		}()
		MustLookup("")
	})
}

func TestNormalise(t *testing.T) {
	tests := []struct{ in, want string }{
		{"cad", "CAD"},
		{"CAD", "CAD"},
		{" cAd ", "CAD"},
		{"\tusd\n", "USD"},
		{"", ""},
		{"   ", ""},
	}
	for _, tt := range tests {
		if got := Normalise(tt.in); got != tt.want {
			t.Errorf("Normalise(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Every launch currency has exponent 2 today. The field exists so that the day
// a currency with a different exponent is added, nothing silently misstates an
// amount by a factor of a hundred — so assert the invariant the registry
// currently relies on, and make its violation a deliberate act.
func TestAllLaunchCurrenciesHaveExponentTwo(t *testing.T) {
	for _, c := range Supported() {
		if c.Exponent != 2 {
			t.Errorf("%s has exponent %d; adding a non-2 currency requires auditing "+
				"every BIGINT column and int64 field in the estate", c.Code, c.Exponent)
		}
	}
}

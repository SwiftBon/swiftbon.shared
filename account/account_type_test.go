package account

import (
	"errors"
	"testing"
)

func TestAccountTypeValues(t *testing.T) {
	// The integers are persisted. If one of these assertions fails, a schema
	// migration is required, not a change to the test.
	want := map[AccountType]int32{
		Savings:           1,
		Checking:          2,
		FixedDeposit:      3,
		SalaryAccount:     4,
		SuspenseAccount:   5,
		CollectionAccount: 6,
		SettlementAccount: 7,
	}
	for a, v := range want {
		if a.EnumIndex() != v {
			t.Errorf("%s: EnumIndex() = %d, want %d", a, a.EnumIndex(), v)
		}
	}
}

func TestStringNeverPanics(t *testing.T) {
	tests := []struct {
		name string
		in   AccountType
		want string
	}{
		{name: "savings", in: Savings, want: "Savings"},
		{name: "checking, not current", in: Checking, want: "Checking"},
		{name: "fixed deposit", in: FixedDeposit, want: "Fixed Deposit"},
		{name: "salary", in: SalaryAccount, want: "Salary Account"},
		// The next three are the values that panicked in the account
		// service, whose names array held only four entries.
		{name: "suspense", in: SuspenseAccount, want: "Suspense Account"},
		{name: "collection", in: CollectionAccount, want: "Collection Account"},
		{name: "settlement", in: SettlementAccount, want: "Settlement Account"},
		{name: "zero", in: 0, want: "AccountType(0)"},
		{name: "negative", in: -1, want: "AccountType(-1)"},
		{name: "above range", in: 8, want: "AccountType(8)"},
		{name: "far above range", in: 9999, want: "AccountType(9999)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("String() panicked on %d: %v", int32(tt.in), r)
				}
			}()
			if got := tt.in.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNamesAreDistinctAndComplete(t *testing.T) {
	if len(names) != int(maxAccountType) {
		t.Fatalf("names has %d entries but the declared range runs to %d", len(names), maxAccountType)
	}
	seen := make(map[string]AccountType, len(names))
	for _, a := range All() {
		n := a.String()
		if prev, dup := seen[n]; dup {
			t.Errorf("name %q is used by both %d and %d", n, prev, a)
		}
		seen[n] = a
	}
}

func TestIsValid(t *testing.T) {
	for _, a := range All() {
		if !a.IsValid() {
			t.Errorf("%d should be valid", a)
		}
	}
	for _, a := range []AccountType{-1, 0, 8, 100} {
		if a.IsValid() {
			t.Errorf("%d should not be valid", a)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      int
		want    AccountType
		wantErr bool
	}{
		{name: "savings", in: 1, want: Savings},
		{name: "checking", in: 2, want: Checking},
		{name: "settlement", in: 7, want: SettlementAccount},
		{name: "zero is rejected, not defaulted", in: 0, wantErr: true},
		{name: "negative is rejected", in: -3, wantErr: true},
		{name: "above range is rejected", in: 8, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrUnknownAccountType) {
					t.Fatalf("Parse(%d) error = %v, want ErrUnknownAccountType", tt.in, err)
				}
				if got != 0 {
					t.Errorf("Parse(%d) = %d alongside an error; must return the zero value", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%d) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%d) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseAcceptsInt32AndInt64(t *testing.T) {
	if got, err := Parse(int32(2)); err != nil || got != Checking {
		t.Errorf("Parse(int32(2)) = %s, %v; want Checking, nil", got, err)
	}
	if got, err := Parse(int64(5)); err != nil || got != SuspenseAccount {
		t.Errorf("Parse(int64(5)) = %s, %v; want Suspense Account, nil", got, err)
	}
}

func TestParseName(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    AccountType
		wantErr bool
	}{
		{name: "exact", in: "Checking", want: Checking},
		{name: "lowercase", in: "checking", want: Checking},
		{name: "padded", in: "  checking  ", want: Checking},
		{name: "deprecated postings spelling", in: "Current", want: Checking},
		{name: "deprecated spelling lowercased", in: "current", want: Checking},
		{name: "multi word", in: "fixed deposit", want: FixedDeposit},
		{name: "multi word with extra spacing", in: "Settlement   Account", want: SettlementAccount},
		{name: "unknown", in: "Chequing", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseName(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrUnknownAccountType) {
					t.Fatalf("ParseName(%q) error = %v, want ErrUnknownAccountType", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseName(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseName(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestAllIsOrderedAndCopied(t *testing.T) {
	all := All()
	if len(all) != int(maxAccountType) {
		t.Fatalf("All() returned %d types, want %d", len(all), maxAccountType)
	}
	for i, a := range all {
		if int(a) != i+1 {
			t.Errorf("All()[%d] = %d, want %d", i, a, i+1)
		}
	}
	all[0] = 99
	if All()[0] != Savings {
		t.Error("All() shares state between calls; it must return a fresh slice")
	}
}

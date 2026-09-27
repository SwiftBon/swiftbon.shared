// Package account holds the account-shape vocabulary shared between the
// services that create accounts and the services that post against them.
//
// It exists because the estate had already drifted: swiftbon.account.service
// declared value 2 as Checking, swiftbon.postings.service declared the same
// value as Current, and the account service's String() indexed a four-element
// array with values that run to seven, so AccountType(5).String() panicked.
// One integer with two names in two services is a bug waiting for the first
// report that joins them.
package account

import (
	"fmt"
	"strings"
)

// AccountType is the kind of account a balance is held in.
//
// The integer values are load-bearing: they are persisted in
// account_management.customer_accounts.account_type and
// account_management.system_accounts.account_type, and they cross the wire in
// CreateSystemAccountRequest. Never renumber them. New kinds append.
type AccountType int32

const (
	// Savings is the default retail deposit account.
	Savings AccountType = iota + 1

	// Checking is the day-to-day transaction account.
	//
	// This is the resolution of the Checking/Current drift. Checking wins:
	// Swiftbon is a Canadian MSB, "current account" is British and Nigerian
	// usage, and the account service — which owns the row — already called it
	// Checking. postings.service called value 2 Current, but it never
	// persisted or transmitted the name: the only reference is the String()
	// method itself, and every comparison there is against the integer via
	// EnumIndex(). So no rows carry the old name and there is nothing to
	// migrate; adopting this type is a rename in postings, not a data change.
	Checking

	// FixedDeposit is a term deposit.
	FixedDeposit

	// SalaryAccount is a payroll account.
	SalaryAccount

	// SuspenseAccount holds amounts in transit that are not yet a customer's.
	SuspenseAccount

	// CollectionAccount receives inbound collections before settlement.
	CollectionAccount

	// SettlementAccount is the counterparty settlement account.
	SettlementAccount
)

// minAccountType and maxAccountType bound the declared range. They are derived
// from the constants rather than written out, so appending a kind below
// SettlementAccount is the only edit an addition needs.
const (
	minAccountType = Savings
	maxAccountType = SettlementAccount
)

// names is indexed by AccountType-1. Keep it in step with the constants; the
// test asserts every declared value has a distinct name.
var names = [...]string{
	"Savings",
	"Checking",
	"Fixed Deposit",
	"Salary Account",
	"Suspense Account",
	"Collection Account",
	"Settlement Account",
}

// String returns the display name, or a diagnostic rendering for a value
// outside the declared range.
//
// It does not panic. The predecessor indexed a four-element array with values
// up to seven, so String() on a suspense, collection or settlement account
// crashed the caller — in a log line, which is the one place a value nobody
// planned for is most likely to arrive.
func (a AccountType) String() string {
	if !a.IsValid() {
		return fmt.Sprintf("AccountType(%d)", int32(a))
	}
	return names[a-1]
}

// IsValid reports whether a names a declared account type.
func (a AccountType) IsValid() bool {
	return a >= minAccountType && a <= maxAccountType
}

// EnumIndex returns the persisted integer value.
func (a AccountType) EnumIndex() int32 { return int32(a) }

// ErrUnknownAccountType is returned by Parse and ParseName for a value outside
// the declared range.
var ErrUnknownAccountType = fmt.Errorf("account: unknown account type")

// Parse converts a persisted or wire integer into an AccountType.
//
// An unknown value is an error, never a default. The predecessor coerced
// anything it did not recognise to Savings, which turned a malformed request
// into a silently mis-typed account rather than a rejected one.
func Parse[T ~int | ~int32 | ~int64](v T) (AccountType, error) {
	a := AccountType(v)
	if !a.IsValid() {
		return 0, fmt.Errorf("%w: %d", ErrUnknownAccountType, int64(v))
	}
	return a, nil
}

// ParseName resolves a display name, case- and space-insensitively. It accepts
// "Current" as a deprecated alias for Checking so that anything holding the old
// postings.service spelling — a config file, a hand-written fixture — resolves
// to the same value rather than failing obscurely.
func ParseName(s string) (AccountType, error) {
	norm := strings.Join(strings.Fields(strings.ToLower(s)), " ")
	if norm == "current" {
		return Checking, nil
	}
	for i, n := range names {
		if norm == strings.ToLower(n) {
			return AccountType(i + 1), nil //nolint:gosec // i is bounded by len(names)
		}
	}
	return 0, fmt.Errorf("%w: %q", ErrUnknownAccountType, s)
}

// All returns every declared account type, in value order, for API discovery
// and validation. The slice is built fresh per call.
func All() []AccountType {
	out := make([]AccountType, 0, len(names))
	for a := minAccountType; a <= maxAccountType; a++ {
		out = append(out, a)
	}
	return out
}

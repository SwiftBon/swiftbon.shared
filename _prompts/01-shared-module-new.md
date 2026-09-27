# 01 — `swiftbon.shared` (new module) — the money contract

> Read `00-READ-FIRST-shared-context.md` first.
> **Run this prompt before every other prompt in this folder.**

---

## Prompt

You are working in `/Users/princeokechukwu/source/repos/swiftbon/`.

Create a new Go module, `swiftbon.shared`, at
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.shared/`.

### Why this exists

Swiftbon is a Canadian licensed MSB adding multi-currency accounts and FX to a codebase
written for a single-currency Nigerian product. There are 14 service repos.

**There is currently no shared Go module.** I verified this: zero `replace` directives,
zero cross-repo imports. `pkg/httpserver`, `pkg/grpcserver`, `pkg/logger`,
`pkg/postgres`, `internal/models/baseResponse.go`, `internal/enums/*`,
`internal/middleware/authorize.go`, `internal/util/token.go`, and the `.proto` files are
all **copy-pasted into every repo** and have already drifted — `account.service` and
`postings.service` disagree on `AccountType` (`Checking` vs `Current`) for the same
integer value.

A `Money` type copy-pasted 14 times will diverge, and when the definition of an amount
diverges between two services in a money path, the ledger is wrong. That is the failure
this module prevents.

### Scope

This module owns exactly three things:

1. **The money and currency model.** The single definition of what an amount is.
2. **The cross-service contracts.** The `.proto` files, in one place, versioned.
3. **The FX rate and quote types** shared between `fx.service` and `postings.service`.

Do **not** move `httpserver`, `grpcserver`, `postgres`, `logger`, middleware, or the
response envelope into it in this change. Those are a worthwhile follow-up but they are
a large blast radius and they are not on the critical path for multi-currency. Keeping
this module small is what makes the other 14 prompts adoptable.

### Layout

```
swiftbon.shared/
  go.mod                       module swiftbon.com/shared
  money/
    money.go                   Money value type
    currency.go                ISO 4217 registry
    convert.go                 conversion + rounding
    format.go                  display formatting
    money_test.go
    currency_test.go
    convert_test.go
  fx/
    quote.go                   Quote, Rate types
    quote_test.go
  proto/
    money/v1/money.proto       Money, Currency on the wire
    fx/v1/fx.proto             quote request/response
    account/v1/account.proto   moved from account.service
    postings/v1/postings.proto moved from postings.service
    seahorse/v1/seahorse.proto moved from seahorse
    gen/go/...                 generated Go, committed
  Makefile                     generate-protoc, test, audit
  README.md
```

### 1. `money` package

```go
package money

// Money is an exact amount in the minor units of Currency.
// {Amount: 12345, Currency: "CAD"} is $123.45.
type Money struct {
    Amount   int64  `json:"amount"`
    Currency string `json:"currency"`
}
```

Amounts are `int64` minor units — do not introduce decimal or float for amounts. All
five launch currencies have exponent 2, so the existing `BIGINT` columns and `int64`
fields across the estate stay correct.

Required API:

```go
func New(amount int64, currency string) (Money, error)  // validates currency
func Zero(currency string) Money
func (m Money) Add(o Money) (Money, error)      // error if currencies differ
func (m Money) Sub(o Money) (Money, error)      // error if currencies differ
func (m Money) Neg() Money
func (m Money) IsZero() bool
func (m Money) IsNegative() bool
func (m Money) Cmp(o Money) (int, error)        // error if currencies differ
func (m Money) String() string                  // "123.45 CAD" — logs, not UI
func (m Money) Validate() error
```

**Mismatched currencies must return an error, never panic and never coerce.** This is
the guard that stops a CAD amount being added to a USD balance somewhere in the estate.

Also provide overflow-checked arithmetic — `Add`/`Sub` must detect `int64` overflow and
error rather than wrap. The current code does raw integer arithmetic with no guards
(e.g. `withdrawableBalance := ledgerBalance + totalPositiveDebts - totalNegativeDebts`
in `postings/internal/usecase/processTransfer.go`).

### 2. `currency.go`

```go
type Currency struct {
    Code     string // ISO 4217 alpha-3, uppercase
    Exponent int32  // decimal places in the minor unit
    Name     string
}

func Lookup(code string) (Currency, error)
func IsSupported(code string) bool
func Supported() []Currency        // sorted by Code, for API discovery endpoints
func MustLookup(code string) Currency  // panics; init/test only
```

Registry: `CAD` 2, `USD` 2, `NGN` 2, `GBP` 2, `EUR` 2.

- Normalise to uppercase on ingest; trim whitespace.
- Unknown or empty code → error. **Never default.** A missing currency is a validation
  failure, not a defaultable value.
- Carry `Exponent` even though all five are 2. It is the reason adding JPY (0) or KWD (3)
  later will not silently corrupt every amount in the system.

### 3. `convert.go`

```go
// Rate is an exchange rate. Decimal, never float.
type Rate = decimal.Decimal   // github.com/shopspring/decimal

// Convert applies rate to m, returning the destination amount and the
// rounding residual expressed in destination minor units.
//
//   destMinor = round_half_up(srcMinor × rate × 10^(destExp − srcExp))
//
// residual is the signed difference between the exact and rounded result,
// and MUST be posted to an FX rounding account so the double entry balances.
func Convert(m Money, to Currency, rate Rate) (converted Money, residual Money, err error)
```

Rates are the **only** decimals in this module. Amounts stay integers.

Round half-up, explicitly. Do not rely on `int64(f * 100)` truncation — that is an
existing bug in `paylony/internal/usecase/worker/inboundTransaction.go:40-46`, where
`1.15 * 100` evaluates to `114.999…` and truncates to `114`.

Test the rounding boundaries directly: exact `.5` cases in both directions, negative
amounts, and a rate that produces a residual.

### 4. `fx` package

Types shared between `swiftbon.fx.service` (producer) and `swiftbon.postings.service`
(consumer), so the two cannot disagree about what a quote is:

```go
type Quote struct {
    ID           string
    Sell         money.Money
    Buy          money.Money
    MidRate      decimal.Decimal
    Rate         decimal.Decimal   // mid adjusted by spread
    SpreadBps    int32
    RateSource   string
    SourceRateID string
    CreatedAt    time.Time
    ExpiresAt    time.Time
}

func (q Quote) IsExpired(now time.Time) bool
func (q Quote) Validate() error   // currencies supported, amounts positive, buy≠sell
```

### 5. `proto` — one home for the contracts

Today the same `.proto` is copied into several repos: `account.proto` lives in
`account.service/pkg/grpcserver/` and is duplicated as
`pkg/grpcserver/clients/account.proto` in `postings`, `kyc`, `communication`, `juice`,
and `paylony`. Any change must currently be applied by hand in six places.

Move the canonical `.proto` files here, namespace them (`money/v1`, `account/v1`,
`postings/v1`, `fx/v1`, `seahorse/v1`), and commit the generated Go.

Define money on the wire once:

```protobuf
// proto/money/v1/money.proto
message Money {
  int64  amount   = 1;  // minor units
  string currency = 2;  // ISO 4217 alpha-3
}
```

Then, in `postings/v1/postings.proto`, replace bare `int64 amount` fields with
`money.v1.Money`. Reserve the old field numbers — do not reuse them:

```protobuf
message PostingRecord {
  reserved 3;                     // was: int64 amount
  money.v1.Money amount = 12;
  // ...
}
```

Add `optional string fx_quote_id` to the posting request.

**Migration note for whoever adopts this.** Changing a field from `int64` to a message
is wire-incompatible. Because every internal gRPC call is a fresh dial with a 1-second
timeout and there is no service mesh, a mixed-version window will fail closed rather
than corrupt data — but it will fail. Sequence the rollout: deploy new `postings` with
handlers that accept **both** the reserved scalar and the new message for one release,
then migrate callers, then drop the scalar. Note this explicitly in the README.

### 6. Wiring into consumers

Each service adopts the module with a `replace` directive, since `swiftbon.com/shared`
is not a resolvable path:

```
// go.mod
require swiftbon.com/shared v0.0.0
replace swiftbon.com/shared => ../swiftbon.shared
```

Add exactly this to each repo's `go.mod` as you work through the other prompts. If the
team would rather publish to a private registry or a monorepo, that is a better end
state — flag it, but do not block on it.

### Constraints

- Go version: match the newest in the estate, `1.24.1`. Several repos are older
  (`bani` 1.22.2, `chatbot` 1.22.2, `juice` 1.23.5) — note in the README that they need
  a `go` directive bump to consume this.
- No dependency on any Swiftbon service. This module must not import anything from
  `internal/` in any repo.
- Only two external dependencies: `github.com/shopspring/decimal` and the protobuf
  runtime. Nothing else.

### Testing

This module is the foundation for a money path, and the estate currently has **no unit
tests at all**. Set the standard here.

- Table-driven tests for every exported function.
- Property tests for the arithmetic invariants: `Add` is commutative;
  `m.Add(n).Sub(n) == m`; `Convert` then converting back at the inverse rate returns to
  within one minor unit.
- Explicitly test every error path: currency mismatch, unknown currency, empty currency,
  lowercase currency, `int64` overflow.
- `make test` must run with `-race` and report coverage. Target ≥90% on `money/`.

### Acceptance criteria

- [ ] `swiftbon.shared` builds and `make test` passes with `-race`.
- [ ] `money.Add` on mismatched currencies returns an error; there is a test proving it.
- [ ] `money.Lookup("")` and `Lookup("XYZ")` error; `Lookup("cad")` returns CAD.
- [ ] `Convert` returns a residual that, added back, reconstructs the exact value; there
      is a test with a rate that forces a non-zero residual.
- [ ] Overflow on `Add` errors rather than wrapping.
- [ ] `.proto` files generate cleanly and the generated Go is committed.
- [ ] README states the `replace` directive, the Go version requirement, and the
      `int64` → `Money` proto migration sequence.

### Out of scope

- Moving `pkg/*`, middleware, or `BaseResponse` into the module.
- Publishing to a registry.
- Changing any of the 14 service repos. This prompt only creates the module; the other
  prompts adopt it.

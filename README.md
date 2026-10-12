# swiftbon.shared

The single definition of what an amount is, and one home for the contracts
services use to talk about amounts.

Swiftbon is adding multi-currency accounts and FX to a codebase written for a
single-currency Nigerian product. Across fourteen service repositories there is
currently no shared Go module: `pkg/httpserver`, `pkg/logger`, the response
envelope, the enums and the `.proto` files are all copy-pasted, and they have
already drifted — `account.service` and `postings.service` disagree on what
`AccountType` value `2` means, one calling it `Checking` and the other `Current`,
and they do not even agree on the underlying type (`int` against `int16`).

A `Money` type copy-pasted fourteen times will drift the same way. The
difference is the consequence: when two services in a money path disagree about
what an amount is, the ledger is wrong. This module exists to make that
particular drift impossible.

## What is in scope

This module owns exactly four things:

1. **The money and currency model** — `money/`
2. **The cross-service contracts** — `proto/`
3. **The FX rate and quote types** shared by `fx.service` and `postings.service` — `fx/`
4. **The account-shape vocabulary** shared by `account.service` and
   `postings.service` — `account/`

`account/` was added when `swiftbon.account.service` adopted this module. It is
here for the same reason `money/` is: the two services had already assigned two
different names to one persisted integer.

It deliberately does **not** contain `pkg/httpserver`, `pkg/grpcserver`,
`pkg/postgres`, `pkg/logger`, the auth middleware or `BaseResponse`. Those are
worth consolidating too, but they have a large blast radius and none of them are
on the critical path for multi-currency. Keeping this module small is what makes
it adoptable one service at a time.

## Adopting it

Releases are git tags on this repository (`v0.1.0`, `v0.2.0`, …), each with a
GitHub Release listing what changed. `swiftbon.com/shared` is not itself a
resolvable import path — nothing at `swiftbon.com` points Go at this repo — so a
consumer keeps that import path and maps it to a tagged release with a `replace`:

```
// go.mod
require swiftbon.com/shared v0.0.0

replace swiftbon.com/shared => github.com/SwiftBon/swiftbon.shared v0.1.0
```

The `replace` line is the only place the version lives; `require` stays at
`v0.0.0`. Imports are unchanged — `swiftbon.com/shared/money`,
`swiftbon.com/shared/proto/gen/go/fx/v1`, and so on. To move to a newer
release:

```sh
go mod edit -replace=swiftbon.com/shared=github.com/SwiftBon/swiftbon.shared@v0.2.0
go mod tidy
```

### Access

The repository is private, so Go must fetch it directly rather than through
`proxy.golang.org`, and git needs credentials for it.

Locally, once:

```sh
go env -w GOPRIVATE=github.com/SwiftBon/*
gh auth setup-git    # or an SSH insteadOf rule for github.com/SwiftBon/
```

In a consumer's GitHub Actions workflow, that repo's `GITHUB_TOKEN` cannot read
this one. Use a fine-grained token with read-only **Contents** access to
`swiftbon.shared`, stored as a secret (`SHARED_READ_TOKEN` below), before any
`go` command:

```yaml
env:
  GOPRIVATE: github.com/SwiftBon/*
steps:
  - run: git config --global url."https://x-access-token:${{ secrets.SHARED_READ_TOKEN }}@github.com/SwiftBon/".insteadOf "https://github.com/SwiftBon/"
```

A Docker build that runs `go mod download` needs the same token, passed as a
build secret rather than a build arg so it does not land in an image layer.

### Local development against an unreleased change

To try a change here from a consumer before releasing it, point the `replace`
at the sibling checkout instead, and put it back before merging:

```
replace swiftbon.com/shared => ../swiftbon.shared
```

### Dropping the `replace`

If `https://swiftbon.com/shared?go-get=1` served

```html
<meta name="go-import" content="swiftbon.com/shared git https://github.com/SwiftBon/swiftbon.shared">
```

consumers could `go get swiftbon.com/shared@v0.1.0` with
`GOPRIVATE=swiftbon.com/*` and no `replace` at all. The page must be public;
the repository can stay private. Nothing else in this module changes.

### Go version

The module's `go` directive is **1.24.1**, matching the newest in the estate.
Four repos are below it and need a `go` directive bump before they can consume
this module:

| Repo | Current | Needs |
| --- | --- | --- |
| `swiftbon.bani.service` | 1.22.2 | 1.24.1 |
| `swiftbon.chatbot.whatsapp.service` | 1.22.2 | 1.24.1 |
| `swiftbon.juice.service` | 1.23.5 | 1.24.1 |
| `seahorse` | 1.23.5 | 1.24.1 |

`swiftbon.paylony.service` is already on 1.25.2 and needs no change; a consumer
may be newer than the module.

### Dependencies

The module depends on `github.com/shopspring/decimal` (already used in the
estate at v1.4.0) and the protobuf runtime.

It also pulls in **`google.golang.org/grpc`**, which is one more than the two
originally intended. This is not optional: the `.proto` files define services,
and the generated client and server stubs import the gRPC runtime. The
alternative — shipping only message types and having each repo generate its own
stubs — reintroduces exactly the per-repo codegen drift this module exists to
remove. Every consumer already depends on gRPC, so the module adds no new
transitive surface to any of them.

Nothing here imports anything from any Swiftbon service, and nothing here
imports any repo's `internal/` package. That is a hard constraint: this module
sits below every service, so a dependency in the other direction would make the
whole thing uncompilable.

## `money`

```go
type Money struct {
    Amount   int64  `json:"amount"`
    Currency string `json:"currency"`
}
```

`{Amount: 12345, Currency: "CAD"}` is $123.45.

**Amounts are `int64` minor units.** Not float, not decimal. All five launch
currencies have exponent 2, so the existing `BIGINT` columns and `int64` fields
across the estate stay correct as they are.

**Mismatched currencies always return an error.** Never a panic, never a
coercion. This is the guard that stops a CAD amount being added to a USD
balance:

```go
cad := money.Money{Amount: 10_000, Currency: "CAD"}
usd := money.Money{Amount: 10_000, Currency: "USD"}

_, err := cad.Add(usd)   // errors.Is(err, money.ErrCurrencyMismatch)
```

**Arithmetic is overflow-checked.** `Add` and `Sub` detect `int64` overflow and
return `money.ErrOverflow` rather than wrapping. Today's code does raw integer
arithmetic with no guard at all — `withdrawableBalance := ledgerBalance +
totalPositiveDebts - totalNegativeDebts` in
`postings/internal/usecase/processTransfer.go:488` will silently produce a negative
withdrawable balance from three large positives. A wrapped balance is worse than
a failed request.

### The representable range

`Money` holds any `int64` **except** `math.MinInt64`. The range is deliberately
symmetric — `MinAmount = -MaxAmount` — so that `Neg` is exact for every value a
`Money` can hold and needs no error return. In minor units the range still spans
roughly ±92 quadrillion major units.

### Currency registry

```go
type Currency struct {
    Code     string // ISO 4217 alpha-3, uppercase
    Exponent int32  // decimal places in the minor unit
    Name     string
}
```

| Code | Exponent | Name |
| --- | --- | --- |
| CAD | 2 | Canadian Dollar |
| USD | 2 | United States Dollar |
| NGN | 2 | Nigerian Naira |
| GBP | 2 | Pound Sterling |
| EUR | 2 | Euro |

Codes are normalised on ingest — trimmed and uppercased — so `Lookup("cad")`
returns CAD. An unknown or empty code is **always an error and never defaulted**.
A missing currency is a validation failure, not a defaultable value: substituting
a house currency turns a caller's omission into a silently mispriced ledger
entry.

`Exponent` is carried even though all five currencies are 2. It is the reason
adding JPY (0) or KWD (3) later will not silently misstate every amount in the
system by two orders of magnitude.

### Conversion and rounding

```go
func Convert(m Money, to Currency, rate Rate) (converted Money, residual Money, err error)
func ConvertExact(m Money, to Currency, rate Rate) (converted Money, residual decimal.Decimal, err error)
```

    exact     = srcMinor × rate × 10^(destExp − srcExp)
    destMinor = round_half_up(exact)
    residual  = exact − destMinor

Rates are the only decimals in this module; amounts stay integers. Rounding is
**half-up and explicit**. It is never `int64(f * 100)` — that truncation is a
live bug in `paylony/internal/usecase/worker/inboundTransaction.go:46`, where
`1.15 * 100` evaluates to `114.999…` in binary floating point and the cast
throws the remainder away, turning $1.15 into 114 cents.

**A note on the residual, because the shape of it matters.** `exact − round(exact)`
is by construction strictly within half a minor unit. A single conversion
therefore cannot discard a whole minor unit, and `Convert`'s `Money` residual is
always zero. The value that was actually discarded lives in the *fraction*, which
is why two forms exist:

- `Convert` returns the payable amount and a `Money` residual. Use it when you
  only need the amount.
- `ConvertExact` returns the residual unrounded, as a `decimal` count of
  destination minor units. `converted.Amount + residual` reconstructs the exact
  value with nothing lost. Use it anywhere the books must reconcile.

`money.Residuals` accumulates those fractions across a batch and hands back whole
minor units once they add up to something postable:

```go
acc, _ := money.NewResiduals("USD")
for _, t := range batch {
    converted, residual, _ := money.ConvertExact(t.Amount, usd, quote.Rate)
    acc.Add("USD", residual)
    post(converted)
}
rounding, _ := acc.Drain()   // whole minor units for the FX rounding account
post(rounding)               // the sub-unit remainder is retained, not dropped
```

A third of a cent dropped ten thousand times is thirty-three dollars missing
from the FX position with no entry explaining it. `Residuals` is the mechanism
that prevents that; `Drain` retains the sub-unit remainder for the next batch.

### Round-trip accuracy

Converting and converting back at the inverse rate lands within one minor unit
**when the rate is at or above 1**, which every launch pair is. It cannot be
exact — rounding to a whole minor unit is lossy by definition.

For rates below 1 the true bound is wider: `0.5/rate + 0.5` source minor units,
because one destination minor unit is worth `1/rate` source ones. At a rate of
0.00065, converting ₦1.00 to USD and back really does return zero, since a
hundredth of a US cent cannot be held. That is not a defect — it is the reason
the residual exists. The tests assert the honest bound rather than one that
happens to hold for today's pairs.

## `fx`

`Quote` and its validation are shared between `swiftbon.fx.service`, which
produces quotes, and `swiftbon.postings.service`, which consumes them, so the
two cannot disagree about what a quote is.

`Sell` is what the customer gives up; `Buy` is what they receive. Naming the two
sides rather than "from" and "to" keeps the direction unambiguous on both sides
of the wire. `Rate` is the price actually honoured — `MidRate` adjusted by
`SpreadBps` — and is the number postings must use. `MidRate` and `SpreadBps` are
carried alongside so the margin on a trade can be reconstructed later without
re-querying the rate provider.

`IsExpired` treats the expiry boundary as **inclusive**: a quote is expired at
exactly its expiry instant, not a moment after. Honouring a price for even an
instant past its stated life is how a stale rate reaches a posting.

`Validate` deliberately does **not** check that `Buy == Sell × Rate`. A quote
may carry a fee or a tiered price, and asserting the identity here would reject
legitimate quotes.

## `account`

`AccountType` is the kind of account a balance is held in. The integer values
are persisted in `account_management.customer_accounts.account_type` and
`account_management.system_accounts.account_type` and travel in
`CreateSystemAccountRequest`, so they are load-bearing: never renumber them, only
append.

**The `Checking`/`Current` drift is resolved here, in favour of `Checking`.**
`swiftbon.account.service` declared value `2` as `Checking`;
`swiftbon.postings.service` declared the same value as `Current`. `Checking`
wins because the account service owns the row, and because "current account" is
British and Nigerian usage for a business that is now a Canadian MSB.

No data migration is needed for this. postings never persisted or transmitted
the *name*: the only reference to `Current` in that repo is the `String()` method
itself, and every comparison in `postTransaction.go` goes through the integer via
`EnumIndex()`. Adopting this type in postings (prompt `03`) is a rename, not a
backfill. `ParseName` accepts `"Current"` as a deprecated alias so any
hand-written fixture or config carrying the old spelling still resolves.

Two behaviours differ deliberately from the enums they replace:

- **`String()` does not panic.** Both predecessors indexed a fixed array by
  `value-1`, and the account service's array held four entries for a range that
  runs to seven — so `AccountType(5).String()` crashed the caller, in a log line,
  which is exactly where an unplanned-for value is most likely to surface. An
  out-of-range value now renders as `AccountType(5)`.
- **`Parse` errors on an unknown value instead of defaulting to `Savings`.** The
  old `GetAccountType` coerced anything unrecognised, which turned a malformed
  request into a silently mis-typed account rather than a rejected one.

## `proto`

The same `.proto` was previously copied into several repos. `account.proto`
lives in `account.service/pkg/grpcserver/` and is duplicated as
`pkg/grpcserver/clients/account.proto` in postings, kyc, communication, juice,
paylony, billers and flights — any change had to be applied by hand in seven
places. `seahorse.proto` was in ten.

The canonical files now live here, namespaced and versioned:

```
proto/money/v1/money.proto        Money, Currency on the wire
proto/fx/v1/fx.proto              Quote, GetQuote, ConsumeQuote, GetRates
proto/account/v1/account.proto    moved from account.service
proto/postings/v1/postings.proto  moved from postings.service
proto/seahorse/v1/seahorse.proto  moved from seahorse
proto/gen/go/...                  generated Go, committed
```

### `fx.v1`

`FxService` is what `swiftbon.fx.service` serves and `postings.service` calls:

| RPC | |
| --- | --- |
| `Quote` | Price a trade, fixing exactly one side (`sell` or `buy`), and issue a single-use quote. |
| `GetQuote` | Fetch a quote by id, with its status (open / consumed / expired). |
| `ConsumeQuote` | Spend a quote against a posting reference. Idempotent per reference (`replayed`). |
| `GetRates` | Indicative customer rates for every direction. Nothing can be posted against them. |

A quote's `buy` is always exactly `money.Convert(sell, buy currency, rate)`, so
a consumer that recomputes it gets the same number. Rates are 10-place decimal
strings. Errors are gRPC statuses carrying a `google.rpc.ErrorInfo` with domain
`fx.swiftbon.com`; branch on its `reason` (`QUOTE_EXPIRED`, `QUOTE_CONSUMED`,
`RATE_UNAVAILABLE`, …), never on the message. The reason table is at the top of
the `.proto`.

This replaced an earlier draft of the contract (`GetQuote` that priced, plus
`GetRate` and `ListCurrencies`) before anything deployed against it.

Money is defined once:

```protobuf
message Money {
  int64  amount   = 1;  // minor units
  string currency = 2;  // ISO 4217 alpha-3
}
```

An amount is never a bare `int64` in a Swiftbon contract. A number with no
currency attached is a number whose meaning depends on which service is reading
it, and that ambiguity is exactly how a CAD amount gets debited from a USD
balance.

### Field naming

Field *names* are snake_case here where the originals used PascalCase. This is a
rename only — field numbers are unchanged, so the wire format is identical, and
`protoc-gen-go` derives the same exported Go identifiers from either spelling.
No Go call site changes because of it. JSON/JSONPB field names do change, which
matters only if something transcodes these messages to JSON; nothing currently
does.

### Regenerating

```sh
make protoc-tools     # installs protoc-gen-go and protoc-gen-go-grpc at pinned versions
make generate-protoc  # regenerates proto/gen/go
```

`protoc` itself must be on your PATH (`brew install protobuf`). The generated Go
is committed, so consumers never need protoc to build. `make generate-check`
fails if the committed output has gone stale.

## ⚠️ Migrating `int64` amounts to `money.v1.Money` on the wire

**This is a wire-incompatible change and must be sequenced.**

In `postings/v1/postings.proto` the bare `int64` amount fields have been replaced
by `money.v1.Money`, and the old field numbers are **reserved, never reused**:

```protobuf
message PostingRecord {
  reserved 3;                     // was: int64 Amount
  reserved "Amount";

  money.v1.Money amount = 12;
  // ...
}
```

Reserved: `ProcessTransferRequest` 5, `TransactionFee` 1, `PostingRecord` 3,
`PostingMetadata` 7.

Changing a field from a scalar to a message changes its wire type, from varint
to length-delimited. A new server reading an old client's varint on field 3 does
not decode it as a `Money` — it fails to parse.

**Failing is the good outcome here.** Because every internal gRPC call is a
fresh dial with a one-second timeout and there is no service mesh holding long-
lived connections, a mixed-version window fails closed rather than corrupting
data. But it *will* fail, visibly, for as long as the window is open. So:

1. **Deploy `postings` first**, with handlers that accept **both** the reserved
   scalar and the new `Money` message, for one full release. Read the scalar
   when the message is absent, treat the account's own currency as its
   denomination, and log every time the scalar path is taken so you can watch it
   drain to zero.
2. **Migrate the callers** — billers, flights, juice, paylony, paystack — one at
   a time, each sending `money.v1.Money`. Watch the scalar-path counter on
   `postings` fall to zero and stay there.
3. **Then drop the scalar** handling from `postings` and leave the field numbers
   reserved forever.

Do not compress steps 1 and 2 into one deploy. The reserved field numbers exist
precisely so that a rolled-back client cannot be misread by a rolled-forward
server: a reused number would be decoded as the wrong field rather than
rejected, and *that* is how a fee becomes an amount.

`fx_quote_id` is `optional` on both `ProcessTransferRequest` and
`PostingRequest`, so it is additive and needs no sequencing. When it is set,
`postings` must re-check the quote is unexpired before posting — a quote that
lapsed between pricing and posting is a rate nobody agreed to.

### Still outstanding

`account/v1`'s `initial_balance` is still a bare `int64`. Giving it a currency is
the obvious next change, but it belongs with the account service's own
multi-currency work rather than with the creation of this module, so the field
is carried over unchanged for now.

## Testing

The estate currently has no unit tests at all. This module sets the standard,
because it is the foundation for a money path.

```sh
make check    # build, vet, test with -race, and enforce the coverage floor
make test     # just the tests, with -race
make audit    # go mod verify, dependency surface, govulncheck
```

Current state: **100% statement coverage** on `money/` and `fx/`, with `-race`.
The Makefile enforces a 90% floor and fails the build below it.

What is covered:

- Table-driven tests for every exported function.
- Property tests for the arithmetic invariants: `Add` is commutative and
  associative; `m.Add(n).Sub(n) == m`; `Sub` agrees with `Add(Neg)`; `Cmp` is
  antisymmetric; converting and converting back at the inverse rate returns
  within the rounding tolerance.
- Every error path: currency mismatch on every operation for every pairing of
  the five currencies, unknown currency, empty currency, un-normalised currency,
  `int64` overflow in both directions, and the exact edges of the representable
  range.
- The rounding boundaries directly: exact `.5` in both directions, negative
  amounts, and rates that force a non-zero residual.
- A regression test pinning `1.15 → 115`, so the truncating float path cannot
  come back.

## Releasing

Actions → **cd** → **Run workflow** on `main`, and choose the bump: `patch`
for fixes, `minor` for anything additive or — while on `v0` — breaking. The
workflow (`.github/workflows/cd.yml`) works out the next version from the
latest tag, runs `go mod tidy -diff`, `go mod verify`, `make fmt-check` and
`make check`, and only if all of them pass creates the tag and the GitHub
Release.

Do not push `v*` tags by hand. Go resolves the tag, not the Release, so a
hand-pushed tag is consumable whether or not it passed the gate. A tag ruleset
restricting `v*` creation to the workflow makes this enforced, not just
documented.

A tag is permanent once anyone has fetched it — their `go.sum` pins its
contents. Never move or reuse one; release the fix as the next version.

`v2` and later need the module path to become `swiftbon.com/shared/v2`; the
workflow refuses a `major` bump past `v1` until it has.

## Layout

```
swiftbon.shared/
  go.mod                       module swiftbon.com/shared
  money/
    money.go                   Money value type, overflow-checked arithmetic
    currency.go                ISO 4217 registry
    convert.go                 conversion, rounding, residual accumulation
    format.go                  display formatting and parsing
    money_test.go
    currency_test.go
    convert_test.go
    format_test.go
  fx/
    quote.go                   Quote, spread application
    quote_test.go
  account/
    account_type.go            AccountType, the Checking/Current resolution
    account_type_test.go
  proto/
    money/v1/money.proto
    fx/v1/fx.proto
    account/v1/account.proto
    postings/v1/postings.proto
    seahorse/v1/seahorse.proto
    gen/go/...                 generated Go, committed
  .github/workflows/cd.yml     release: gate, tag, GitHub Release
  Makefile
  README.md
```

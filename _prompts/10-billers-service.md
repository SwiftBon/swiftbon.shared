# 10 — `swiftbon.billers.service` — currency-aware bill payments

> Read `00-READ-FIRST-shared-context.md` first. Requires `01`, `02`, `03`, `15`.

---

## Prompt

You are working in
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.billers.service`
(module `swiftbon.com/billers`, Go 1.24.1).

Swiftbon is a Canadian licensed MSB. This service sells airtime, data, cable/TV bouquets,
electricity, and betting-wallet top-ups through **PayBeta/VTpass**
(`config.yml:74-76`) and **GiftBills** (`:77-82`).

The billers themselves are Nigerian and priced in naira. The change here is not to make
them multi-currency — it is that **a customer holding CAD must be able to pay an NGN
bill**, which means this service becomes the first real consumer of ledger FX.

### Current state (verified)

**It does not touch balances itself** — it calls `postings` over gRPC:

- `internal/usecase/webapi/reserveFunds.go:38-79` → `PostTransaction`, two legs:
  debit the customer (`NominalAccount`, entry `"D"+305`) / credit the Juice Collection
  Account `132558` (`SystemAccount`, `"C"+305`), `PostingType: AccountToGLTransfer`.
- `internal/usecase/webapi/splitRevenue.go:38-79` → GL→GL, Collection `132558` → Revenue
  `161232`, `ForceDebit: true`.
- `internal/usecase/webapi/reverseTransaction.go:36-38` → `ReverseTransaction`.
- `internal/usecase/webapi/GetAccount.go:38` → account service
  `SearchCustomerByAccountNumber`.

**Order of operations** — `internal/usecase/purchase.go:17-77`: dedupe by reference →
`GetAccount` → **`ReserveFunds` (debit first)** → insert `purchases` with `FundReserved`
→ return `00`. The worker (`internal/usecase/worker/processPurchase.go:14-118`) then
calls the aggregator and moves to `Initiated` / `TSQRequired` / `InitiationFailed`.
`processReversal.go` and `processRevenueSplit.go` handle the rest.

**This service already has a reversal path** — unlike `paystack` and `paylony`. Preserve
it.

**Hard NGN, with unit conversion at the aggregator boundary.** `config.yml:8` —
`default_currency: NGN`, `swiftbon_bank_code: 000000`. Amounts are integer kobo
internally; aggregator calls divide by 100 (`processPurchase.go:20,27`) and DB writes
multiply by 100 (`:74-80`). Fee `570` / revenue `430` kobo (`config.yml:24-25`).

`PurchaseRequest` has no currency field.

**Hardcoded GL account numbers.** `132558` (collection) and `161232` (revenue) appear as
literals in `reserveFunds.go` and `splitRevenue.go`. With per-currency GL accounts
(prompt 03 §5.1) a single literal cannot work.

**Config gotcha:** `config.go` declares `Postings{Host,ClientId}` as
`env-required:"true"` but `config/config.yml` has **no `postings:` block** — it only
works because CD injects `POSTING_HOST`/`POSTING_CLIENT_ID`. Local runs fail confusingly.

**Copy-paste rot from postings:** `logger.log_file_location` is
`/home/devops/bin/log/postings.log`, the consumer group is `cba.postings`, the gRPC
server registers as `pb.RegisterPostingsServer` (the proto was copied and never renamed),
docker-compose container names are all `postings-*`, and the `Makefile` has a stray
`run: go run ./cmd/api` for a directory that does not exist.

---

## Tasks

### 1. Adopt `swiftbon.shared`

Add the `replace` directive. Convert amounts to `money.Money`.

**Replace the scattered `/100` and `*100` conversions.** The kobo↔naira conversion is a
property of each aggregator's API, so it belongs in the aggregator adapter, not in
`processPurchase.go` and not in the repository writes. Move it there, make it explicit
per aggregator, and use `money` for the arithmetic. Audit for a truncating division while
you are in there — `paylony` has exactly this bug at
`worker/outboundTransaction.go:18`, and this code does the same `/100` in two places.

### 2. Bill currency vs. funding currency

This is the substantive change. Separate two things the code currently conflates:

- **Bill currency** — what the biller charges. NGN for every current aggregator.
- **Funding currency** — the currency of the account being debited. Any of the five.

`PurchaseRequest` gains an optional `funding_account_number`. Resolve its currency from
the account service (prompt 02 adds `currency` to that response).

**Same currency** (NGN account paying an NGN bill): unchanged flow.

**Cross-currency** (CAD account paying an NGN bill):

1. Compute the bill amount in NGN.
2. Request an `fx.service` quote for funding → bill currency (CAD → NGN).
3. Call postings `PostTransaction` with `fx_quote_id`. Postings produces the balanced
   four-leg conversion and debits the CAD account.
4. Persist `fx_quote_id` and the applied rate on the `purchases` row, so a customer
   query can show what rate they paid.

**Show the customer the funding-currency amount before they commit.** A quote-then-confirm
step is required here in a way it is not for a same-currency purchase: the customer is
agreeing to spend CAD to buy an NGN product, and they must see the CAD figure and the
rate first. Add `POST /billers/v1/api/quote/:providerType` returning the bill amount, the
funding amount, the rate, and the quote expiry — then accept the `quote_id` on the
purchase call.

Do **not** compute the conversion in this service. Postings applies the quote; a second
conversion computed here would disagree with the ledger.

### 3. Per-currency GL accounts

Replace the `132558` and `161232` literals with lookups by (purpose, currency) against
the per-currency chart of accounts from prompt 03 §5.1 — `COLLECTION_<CCY>`,
`REVENUE_<CCY>`.

Add a startup check that resolves every GL account this service needs for every
configured currency and **fails to boot if one is missing**. Discovering a missing
`REVENUE_CAD` at purchase time strands a customer mid-transaction.

### 4. Fees per currency

Fee `570` and revenue `430` are kobo. Applied unchanged to a CAD purchase they become
$5.70 and $4.30 — roughly a 100× overcharge.

Move fees to a per-currency configuration keyed by (currency, provider type). **Reject a
currency with no configured fee** rather than defaulting. Add a floor check so
`amount - fee - revenue` cannot go negative (the bug at
`juice/internal/usecase/webapi/reserveFunds.go:62`; check whether this repo's copy shares
it).

### 5. Aggregator currency validation

Declare what each aggregator settles:

```yaml
aggregators:
  vtpass:    { settlement_currency: 'NGN', countries: ['NG'] }
  giftbills: { settlement_currency: 'NGN', countries: ['NG'] }
```

Validate the bill currency against the chosen aggregator before reserving funds. Never
send an amount to an aggregator in a currency it does not settle.

### 6. Defects and rot to fix in passing

- Add the missing `postings:` block to `config/config.yml` so local runs work.
- `logger.log_file_location` → `billers.log`; consumer group `cba.postings` → `cba.billers`.
- `pb.RegisterPostingsServer` → a proper billers service definition. Prompt 01 moves the
  protos to `swiftbon.shared`; add a `billers/v1` proto there.
- docker-compose container and image names `postings-*` → `billers-*`.
- Remove the stray `run: go run ./cmd/api` Makefile target.
- `md.Get("x-client-id")[0]` panics on metadata without that key.
- `internal/controller/http/v1/billers.go:143-155` does an unchecked
  `err.(validator.ValidationErrors)` type assertion — it panics on a non-validation
  error. Use the checked form.
- HTTP client has no timeout.
- GiftBills webhook — verify signature checking exists; if not, add it (three of the
  five provider services ship without it).
- 24 enum files, most unused, copied from postings. Delete the dead ones.
- README is stale boilerplate.
- `config.yml:74-82` contains live-looking VTpass and GiftBills API keys. Raise
  separately; rotate.
- CI pins Go 1.17 against a `go 1.24.1` module.

---

## Verification

```bash
cd /Users/princeokechukwu/source/repos/swiftbon/swiftbon.billers.service
make compose-up && make migrate-up && make test   # -race
```

With account, postings, and fx running:

- NGN airtime purchase from an NGN account: unchanged behaviour, `00`, fee to
  `REVENUE_NGN`. **Regression test.**
- Quote an NGN airtime purchase funded from a CAD account: returns the bill amount in
  NGN, the funding amount in CAD, the rate, and an expiry.
- Execute that quote: postings shows four balanced legs across CAD and NGN, the CAD
  account is debited the quoted amount, the aggregator receives NGN, and `fx_quote_id`
  plus the rate are on the `purchases` row.
- Reuse the quote: rejected.
- Execute an expired quote: rejected, **no ledger movement**.
- Purchase in a currency with no configured fee: rejected, no ledger movement.
- Aggregator failure after reservation: existing reversal path returns the ledger to its
  prior state — including for a cross-currency purchase, where the reversal must undo all
  four legs.
- Boot with `REVENUE_CAD` deleted: the service refuses to start.

## Acceptance criteria

- [ ] Bill currency and funding currency are separate concepts.
- [ ] A CAD account can pay an NGN bill via a ledger FX quote.
- [ ] No conversion arithmetic in this repo; postings applies the quote.
- [ ] Quote-then-confirm exists and shows the customer the funding amount and rate.
- [ ] GL accounts resolved per currency; startup fails on a missing one.
- [ ] Fees per currency, with a floor check; unconfigured currency rejected.
- [ ] Aggregator settlement currency validated before reserving funds.
- [ ] Cross-currency reversal correctly undoes all four legs.
- [ ] `/100` and `*100` conversions moved into the aggregator adapters and covered by
      tests.
- [ ] Postings copy-paste rot cleaned up (log file, consumer group, proto, compose names).

## Out of scope

- Non-NGN billers. When one is added, the aggregator config already supports it.
- Rate sourcing (`15`), ledger mechanics (`03`).
- Flights — that is `11-flights-service.md`, and it must be done as a re-fork.

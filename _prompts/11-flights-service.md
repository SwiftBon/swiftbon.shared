# 11 — `swiftbon.flights.service` — re-fork from billers, do not patch

> Read `00-READ-FIRST-shared-context.md` first. **Requires `10-billers-service.md` to be
> complete and merged.**

---

## Prompt

You are working in
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.flights.service`.

**Read this before touching anything: this repo is a byte-identical copy of
`swiftbon.billers.service`.** `diff -rq` between the two returns nothing. Verify that
yourself first:

```bash
cd /Users/princeokechukwu/source/repos/swiftbon
diff -rq --exclude=.git swiftbon.billers.service swiftbon.flights.service
```

Confirmed facts:

- `go.mod` still says `module swiftbon.com/billers`.
- sqlc package is `billersDb`; schema file is `internal/entity/sql/schema/billers.sql`.
- Routes are still `/billers/v1/api/...`.
- Swagger title is "Swiftbon Billers Service".
- `config.yml` has `default_currency: NGN` and the billers aggregator config.
- Git history is two commits: `initial commit` and `commit settings`.
- **There is no flights code.** No airline integration, no GDS, no IATA codes, no PNR, no
  fare search, no ticketing, no booking, no cancellation.

This is a fork placeholder. Nothing about flights has been written.

---

## Task 0 — decide, and get an answer before proceeding

Ask the owner: **is flight booking on the roadmap now?**

**If no** — leave the repo alone and note in the PR that it is an empty placeholder
carrying no flights functionality. Do not spend effort making a copy of billers
multi-currency twice. Doing so creates a second divergent copy of code that is about to
be replaced wholesale.

**If yes** — do **not** patch this repo in place. Re-fork it from the *completed*
`swiftbon.billers.service` after prompt 10 merges, then build flights on top. Patching in
place means applying prompt 10's changes a second time by hand to a copy that has already
drifted, and the two will diverge again immediately.

Everything below assumes the answer is yes.

---

## Why flights is genuinely different, and why currency matters more here

Bill payments and flight booking look similar (buy a thing, debit an account) and are not.
Flights is the one product in the estate where **multi-currency is intrinsic rather than
incidental**:

- **Airline inventory is commonly priced in USD or EUR**, not in the customer's currency.
  For billers, the bill currency is always NGN and FX is a convenience. For flights, the
  fare currency is set by the airline and the carrier's settlement currency, and a
  Canadian customer buying a Lagos–London ticket may be quoted in USD, GBP, or NGN
  depending on the carrier and point of sale.
- **Fares are volatile and quotes are short-lived.** A fare quote can expire in minutes.
  Combined with an FX quote that also expires, there are **two** expiry windows to
  reconcile, and the shorter one governs.
- **Ticketing is not instant and not always reversible.** A biller top-up either lands or
  it does not. A ticket goes through booking → hold → payment → ticketing, with a PNR
  that may be held for hours, airline cancellation penalties, and refunds that are partial
  and time-dependent. "Reverse the ledger reservation" is not a sufficient failure model.
- **Taxes and carrier fees** are a large fraction of the total and are quoted separately.
- **Refunds and involuntary changes** arrive days or months later, often at a different FX
  rate than the original sale. The ledger must record the original rate (prompt 03 does
  this) so a refund can be reasoned about.

Treat this as a new product, not a currency patch on billers.

---

## Tasks (if being built)

### 1. Re-fork cleanly

Start from the merged, multi-currency `swiftbon.billers.service` and rename properly:

- `go.mod` → `module swiftbon.com/flights`
- sqlc package `billersDb` → `flightsDb`; regenerate
- `internal/entity/sql/schema/billers.sql` → `flights.sql`
- routes `/billers/v1/api/...` → `/flights/v1/api/...`
- swagger title and description
- `logger.log_file_location` → `flights.log`
- consumer group → `cba.flights`
- gRPC service definition — the current one registers as `pb.RegisterPostingsServer`,
  copied from postings and never renamed. Define `flights/v1` in `swiftbon.shared`
  (prompt 01) rather than copying a proto again.
- docker-compose container and image names (`postings-*` today)
- systemd unit names in `scripts/`
- the CD workflow's deploy target
- delete the billers aggregator config, the billers migrations, and the ~24 copied enum
  files that flights does not use

Assign unused ports and confirm against every repo's `config.yml` first — the estate
already has collisions (billers and postings both claim 8103/8203).

### 2. Adopt `swiftbon.shared`

Already present if you forked from completed billers. Use `money.Money` throughout.

### 3. Fare currency, funding currency, settlement currency

Three distinct currencies, and conflating any two of them will cause a reconciliation
break:

- **Fare currency** — what the airline/GDS quotes. USD, EUR, GBP, NGN, CAD.
- **Funding currency** — the currency of the customer account being debited.
- **Settlement currency** — what Swiftbon actually pays the GDS or consolidator in. Often
  neither of the other two.

Persist all three on the booking, with the FX quote and applied rate for each conversion.

Flow:

1. Fare search returns fares in their native currencies, each with its own expiry.
2. Get an `fx.service` quote for fare currency → funding currency.
3. Present the customer the funding-currency total, the rate, and **the earlier of the two
   expiries**. Say which one it is.
4. On confirm, validate both quotes are still live. If either expired, re-quote and
   re-confirm — never silently re-price. The customer agreed to a number.
5. Reserve funds via postings `PostTransaction` with `fx_quote_id`.
6. Ticket. On failure, reverse.

Do not compute conversions in this service. Postings applies the quote.

### 4. A booking state machine, not a purchase row

Billers' `purchases` table with a status column is not sufficient. Model the lifecycle
explicitly: `searched → held → funds_reserved → paid → ticketed`, plus
`cancelled`, `refund_pending`, `refunded`, `partially_refunded`, `void`, `expired`.

Store the PNR, ticket numbers, fare breakdown (base fare, taxes, carrier fees as separate
`money.Money` values), and the FX quotes used.

**Ticketing failure after payment is the hard case.** The customer is debited, the airline
has the money or a hold, and there may or may not be a PNR. Options are refund, reissue,
or manual intervention, and choosing wrongly either loses the ticket or double-charges.
Design for a **manual resolution queue** with the transaction parked in a suspense state
rather than an automatic reversal. Build the queue; do not automate the decision.

### 5. Refunds at a different rate

A refund arriving weeks later converts at a different rate than the sale. Post the FX
difference to a designated GL account (an FX gain/loss account per currency — add it to
the chart of accounts in prompt 03 §5.1). Do not adjust the customer's refund to hide the
difference: refund what was agreed and let the rate difference land where it belongs.

This needs the original rate, which prompt 03 persists on `posting_history`.

### 6. GDS integration

The provider is a commercial decision. Build against an interface from the start — do not
repeat the four-bespoke-interfaces pattern that makes adding a provider in this estate mean
forking a repo:

```go
type FlightProvider interface {
    Name() string
    SearchFares(ctx context.Context, req FareSearchRequest) ([]Fare, error)
    HoldBooking(ctx context.Context, req HoldRequest) (Booking, error)
    Ticket(ctx context.Context, req TicketRequest) (Ticket, error)
    Cancel(ctx context.Context, req CancelRequest) (CancelResult, error)
    Refund(ctx context.Context, req RefundRequest) (RefundResult, error)
    SettlementCurrency() string
}
```

Include webhook signature verification from the start. Three of the five existing
provider services ship with it absent or commented out; do not make it four.

---

## Verification

**Task 0 outcome "no":** confirm the diff result and that nothing calls this service:
```bash
cd /Users/princeokechukwu/source/repos/swiftbon
grep -rn "flights" --include='*.go' --include='*.yml' . | grep -v "swiftbon.flights.service/"
```

**If built:**
```bash
cd /Users/princeokechukwu/source/repos/swiftbon/swiftbon.flights.service
grep -rn "billers\|postings" --include='*.go' --include='*.yml' . | grep -v vendor
# expect only deliberate postings gRPC client references
make compose-up && make migrate-up && make test   # -race
```

- Search fares: multiple currencies returned with their own expiries.
- Quote a USD fare funded from a CAD account: funding total, rate, and the earlier expiry
  are all shown, and the expiry shown is demonstrably the earlier of the two.
- Confirm within both windows: four balanced legs across CAD and USD in postings, all
  three currencies plus rate persisted on the booking, PNR recorded.
- Let the fare expire but not the FX quote: re-quote required, no silent re-price, no
  ledger movement.
- Let the FX quote expire but not the fare: same.
- Ticketing fails after payment: booking parks in suspense and appears in the manual
  resolution queue. **No automatic reversal.**
- Refund at a materially different rate: customer receives the agreed refund amount and
  the rate difference posts to the FX gain/loss account.

## Acceptance criteria

**If not being built:**
- [ ] Repo untouched; PR notes it is an empty billers copy with no flights code.

**If being built:**
- [ ] Re-forked from completed billers; no `billers` or `postings` identifiers remain
      except deliberate gRPC client calls.
- [ ] Ports do not collide with any existing service.
- [ ] Fare, funding, and settlement currencies are modelled separately and all persisted.
- [ ] Dual-expiry handled; the binding expiry is the earlier one and is shown as such.
- [ ] Expiry forces re-quote and re-confirm; never a silent re-price.
- [ ] Booking state machine covers hold, ticket, cancel, and partial refund.
- [ ] Ticketing-failure-after-payment parks in a manual resolution queue.
- [ ] Refund FX difference posts to an FX gain/loss account; the customer refund is not
      adjusted to absorb it.
- [ ] `FlightProvider` interface with webhook signature verification from day one.

## Out of scope

- Choosing the GDS or consolidator.
- Ancillaries (seats, bags), loyalty, and multi-passenger itinerary complexity — scope
  these separately once the single-passenger money path is correct.

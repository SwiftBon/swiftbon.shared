# 07 — `swiftbon.paystack.service` — currency-aware NGN rail

> Read `00-READ-FIRST-shared-context.md` first. Requires `01`, `02`, `03`, `04`.
> Follow `05-juice-service.md` as the reference implementation — it is the same shape,
> further along.

---

## Prompt

You are working in
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.paystack.service`
(module `swiftbon.com/paystack`, Go 1.24.1).

Swiftbon is a Canadian licensed MSB. This service wraps **Paystack**
(`https://api.paystack.co`) for dedicated NUBAN accounts, transfers, and collections.

**Paystack is a Nigerian provider.** Realistically this service stays NGN-only. That is
fine and expected — the point of this change is **not** to make Paystack multi-currency.
The point is that once the ledger holds five currencies, this service must:

- state its currency explicitly instead of relying on an implicit global assumption,
- **refuse** work in currencies it cannot settle rather than silently treating a CAD
  amount as naira,
- and stop stranding customer funds when a payout fails.

That last one is a live money-loss bug, and it is the most important task here.

### Current state (verified)

**Currency is hardcoded and config-derived.**
- `internal/usecase/webapi/getBankList.go:13` — `GET /bank?currency=NGN&use_cursor=false`
  (NGN in the URL).
- `internal/usecase/webapi/createRecipient.go:27` —
  `request.Currency = api.config.App.DefaultCurrency`.
- `config/config.yml:6` — `default_currency: NGN`, declared `env-required:"false"` so a
  missing key yields `""` silently.

**Provider endpoints:**

| Function | File | Call |
|---|---|---|
| `CreateCustomer` | `webapi/createCustomer.go:13` | `POST /customer` |
| `AssignAccount` | `webapi/assignAccount.go:13` | `POST /dedicated_account` |
| `GetPayoutBankList` | `webapi/getBankList.go:13` | `GET /bank?currency=NGN&…` |
| `DoNameEnquiry` | `webapi/createRecipient.go:13` | `GET /bank/resolve?…` |
| `CreateRecipient` | `webapi/createRecipient.go:28` | `POST /transferrecipient` |
| `InitateOutbound` | `webapi/initiateOutbound.go:13` | `POST /transfer` (`Source: "balance"`) |

**Routes:**
```
POST /paystack/v1/api/recipient
POST /paystack/v1/api/transfer
POST /paystack/v1/api/banklist              (POST for a read)
POST /paystack/v1/api/customer/:accountNumber
POST /paystack-webhook/v1/api/webhook       (separate port, no auth middleware)
```

**Webhook signature verification is entirely absent.**
`internal/usecase/webhook.go:15-37` does no verification at all — there is no
`x-paystack-signature` check anywhere in the repo, and the webhook route group never
calls `customMiddleware.Authorize`. **Anything on the internet can post a
transfer-succeeded or transfer-failed event to this service.**

There is a generic HMAC helper, `util.GenerateEncodedHash` (`internal/util/token.go:133`),
but it is SHA-**256** and its only reference is a **commented-out** `moni-signature`
header for *outbound* requests (`internal/usecase/webapi/paystackUtilities.go:8`). It is
not a webhook verifier. Paystack signs webhooks with HMAC-**SHA512** over the raw body
using the secret key — do not repurpose the SHA-256 helper.

**Reserved funds can be stranded.** `internal/usecase/worker/outboundTransaction.go:63`:
```go
//TODO: publish a failed transaction event
```
and twice in `processWebhook.go` (lines 138 and 163):
```go
//TODO: fire event to reverse funds
```
The flow debits the customer via postings `PostTransaction` **before** calling the
provider (`internal/usecase/outbound.go` → `webapi/reserveFunds.go`). If the provider
call or the payout fails, there is no reversal — the customer is debited and the money
goes nowhere. `juice` has `reverseFailedTransaction.go`; this service does not. There is
also no status polling, so a payout that never gets a webhook stays `Pending` forever.

**Webhook ingest has no idempotency.** `ProcessWebhook` inserts every payload into
`events` unconditionally. Paystack retries webhooks.

**Dead inherited config.** `merchant_key` and `tribe_account_ref` are
`env-required:"true"` (inherited from bani) and unused.

---

## Tasks

### 1. Adopt `swiftbon.shared`; make the currency explicit

Add the `replace` directive. Convert money fields to `money.Money`
(`InitiateOutboundDto.Amount` at `internal/models/dto.go:133`, the webhook's `string`
amount at `internal/models/requestModels.go`, and the DB columns).

Replace `default_currency` with an explicit, non-defaultable declaration of what this
rail settles:

```yaml
paystack:
  settlement_currencies: ['NGN']
```

Then **validate on every entry point**:

- `POST /paystack/v1/api/transfer` — resolve the source account's currency from the
  account service (prompt 02 adds `currency` to that response). If it is not in
  `settlement_currencies`, reject with a validation error **before any ledger
  movement**.
- Same for collections: if an inbound payment's currency is not NGN, route to
  `SUSPENSE_NGN` and raise an investigation event. Do not convert it.

`GET /bank?currency=NGN` stays as-is — it is correct for this provider. Derive the query
parameter from the configured settlement currency rather than a literal, so it reads as
intentional.

Existing `currency` columns become `CHAR(3)` with a check constraint, backfilled to
`NGN`.

**Do not add FX to this service.** Cross-currency conversion happens in the ledger via
`fx.service` quotes (prompts 03 and 15). If a customer wants to pay out NGN from a CAD
balance, postings converts CAD→NGN and this service receives an NGN instruction. This
service never sees two currencies.

### 2. Fix the stranded-funds bug

This is the priority task. Port the pattern from
`swiftbon.juice.service/internal/usecase/worker/reverseFailedTransaction.go` and
`getPendingTransactionStatus.go`.

**Reversal on failure.** Every terminal failure path must reverse the ledger reservation:

- provider call returns an error → reverse
- provider returns a failed status → reverse
- webhook reports failure → reverse
- transaction exceeds a maximum pending age with no resolution → escalate, do not
  auto-reverse blindly (the payout may have succeeded silently; auto-reversing an
  in-flight payout double-spends)

Reversal calls postings `ReverseTransaction{ReferenceNumber}`. Make it idempotent — a
reversal must be safe to retry, and prompt 03 makes postings idempotent on
`(instrument, reference)`.

Replace both `//TODO` comments with real implementations. Do not leave a `TODO` in a money
path.

**Status polling.** Add a worker mirroring `juice`'s `getPendingTransactionStatus.go`
that polls Paystack for transactions stuck in `Pending` past a threshold, so a lost
webhook does not leave a transaction unresolved forever. Paystack's
`GET /transfer/:id_or_code` provides this.

**Reconciliation report.** Add a cron that lists transactions where the ledger and the
provider disagree — reserved but no provider record, or provider succeeded but the ledger
shows failed. An MSB needs this to close its books, and there is nothing today.

### 3. Fix the webhook security hole

Implement Paystack's documented verification:

```go
// HMAC-SHA512 over the RAW request body, keyed by the Paystack secret key,
// compared against the x-paystack-signature header with subtle.ConstantTimeCompare.
```

The raw body is required — Echo will have consumed it if the handler binds first, so
capture the body before binding (`middleware.BodyDump` or read-and-restore
`c.Request().Body`). Getting this wrong produces a verifier that always fails or always
passes; test both directions.

- Reject with `11` on mismatch; log with source IP.
- The secret key must come from the secret store, not `config.yml`.
- Tests: valid accepted, tampered rejected, missing header rejected, valid replay handled
  idempotently.

**Add ingest idempotency** on Paystack's event ID with a unique constraint; treat a
duplicate insert as success.

### 4. Fee and revenue per currency

Fees are configured in kobo (`fee`, `revenue`). Since this rail is NGN-only that is
currently coherent — but make it explicit by keying fees on currency, so a future rail
cannot inherit an NGN fee. Post fee and revenue legs to `REVENUE_NGN` per prompt 03.

Add the floor check that `juice` lacks
(`juice/internal/usecase/webapi/reserveFunds.go:62` subtracts fee and revenue with no
guard, allowing a negative credit leg). Verify whether this repo's copy has the same
defect and fix it if so.

### 5. Defects to fix in passing

- `md.Get("x-client-id")[0]` panics on metadata without that key.
- `internal/util/httpHander.go` [sic — typo in the filename] uses `&http.Client{}` with
  **no timeout**. Set one. Rename the file to `httpHandler.go`.
- Unknown webhook events are marked `EventUnhandled` with `RetryCount+1` and churn
  through all 10 retries. Make unhandled terminal.
- Remove the unused `env-required:"true"` `merchant_key` / `tribe_account_ref`.
- gRPC registers `CreateCustomer` and `AssignAccount` but returns Unimplemented for both;
  only `GetPayoutBankList` works. Either implement or remove them from the proto.
- README is stale boilerplate describing an SMS/email service.
- `config/config.yml` and `.env.example` contain live-looking keys, including
  `sk_live_…` in `.env.example`. Raise separately; rotate.
- CI pins Go 1.17 against a `go 1.24.1` module.

---

## Verification

```bash
cd /Users/princeokechukwu/source/repos/swiftbon/swiftbon.paystack.service
make compose-up && make migrate-up && make test   # -race
```

Against the Paystack sandbox, with account and postings running:

- NGN payout from an NGN account: unchanged behaviour. **Regression test.**
- Payout attempt from a CAD account: rejected before any ledger movement. Assert the CAD
  balance is untouched.
- Provider call fails after reservation: reversal fires, ledger returns to its prior
  state exactly. **This test fails on the current code — it is the regression test for
  task 2.**
- Payout stuck pending with no webhook: polling resolves it.
- Unsigned webhook: rejected with `11`.
- Validly-signed webhook delivered twice: processed once.
- Inbound USD collection: routed to suspense with an event, not converted.
- Reconciliation cron flags a deliberately-created ledger/provider mismatch.

## Acceptance criteria

- [ ] `settlement_currencies` is explicit; nothing defaults to NGN.
- [ ] A non-NGN payout is rejected before ledger movement.
- [ ] A non-NGN collection goes to suspense, never converted.
- [ ] Every failure path reverses the ledger reservation, idempotently. Both `//TODO`s
      are gone.
- [ ] Status polling resolves transactions with no webhook.
- [ ] Reconciliation report exists and detects a seeded mismatch.
- [ ] Webhook verified with HMAC-SHA512 over the raw body; tested both directions.
- [ ] Webhook ingest idempotent on provider event ID.
- [ ] Fee floor enforced; fees keyed by currency; revenue posts to `REVENUE_NGN`.
- [ ] HTTP client has a timeout.

## Out of scope

- Making Paystack settle non-NGN currencies. It does not.
- FX conversion — the ledger does that.
- The shared provider abstraction (note it, do not build it here).

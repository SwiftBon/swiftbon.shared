# 05 — `swiftbon.juice.service` — multi-currency payout rail

> Read `00-READ-FIRST-shared-context.md` first.
> Requires prompts `01`, `02`, `03`, `04`, `15`.

---

## Prompt

You are working in
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.juice.service`
(module `swiftbon.com/juice`, Go 1.23.5 — needs a bump to 1.24.1 to consume
`swiftbon.shared`).

Swiftbon is a Canadian licensed MSB. This service wraps **Juice / SpendJuice**
(`https://api.spendjuice.com`) for virtual accounts, payouts, and collections. It is the
**most developed** of the five provider integrations and the only one whose provider API
has FX-shaped fields — so it is the reference implementation the other four should follow.

### Current state (verified)

**It has the shape for multi-currency and then throws it away.**
`internal/usecase/webapi/initiateOutbound.go:14-15`:

```go
request.SourceCurrency      = api.config.DefaultCurrency
request.DestinationCurrency = api.config.DefaultCurrency
```

Both sides are forced to one config value (`default_currency: NGN`,
`config/config.yml:7`), so the FX-capable provider call can never actually do FX.
`internal/models/requestModels.go:20,23` already has `SourceAmount int64` and
`DestinationAmount int64`.

**Currency is set from config, not from the account.**
`internal/usecase/webapi/createRecipient.go:27` — `request.Currency = api.config.App.DefaultCurrency`.
The config field is `env-required:"false"`, so a missing YAML key silently yields `""`.

**Provider endpoints** (`{host}` = `api.spendjuice.com`):

| Function | File | Call |
|---|---|---|
| `CreateCustomer` | `webapi/createCustomer.go:13` | `POST /customer` |
| `AssignAccount` | `webapi/assignAccount.go:13` | `POST /dedicated_account` |
| `GetPayoutBankList` | `webapi/getBankList.go:13` | `GET /payment-methods/banks` |
| `DoNameEnquiry` | `webapi/createRecipient.go:13` | `POST /payment-methods/resolve-bank-account` |
| `CreateRecipient` | `webapi/createRecipient.go:29` | `POST /beneficiaries` |
| `InitateOutbound` | `webapi/initiateOutbound.go:20` | `POST /payouts` (+ `Idempotency-Key`) |
| `GetTransactionStatus` | `webapi/getTransactionStatus.go:11` | `GET /payments/{id}` |

**Payout flow** — `POST /juice/v1/api/transfer` → `internal/usecase/outbound.go:18`:
dedupe by reference → `webAPI.GetAccount` (account service gRPC) →
`repo.GetRecipientByCode` → `webAPI.ReserveFunds` (postings `PostTransaction`, debit
customer / credit collection) → insert with `TransactionInitiated`. Then
`internal/usecase/worker/outboundTransaction.go:14` calls the provider using the
transaction UUID as the `Idempotency-Key` and moves to `TransactionPending`. Terminal
state arrives via webhook (`worker/processWebhook.go`) or polling
(`worker/getPendingTransactionStatus.go`, matching literal `"success"`/`"failed"`).
Failure compensates via `worker/reverseFailedTransaction.go`.

This is the **only** provider service with polling and reversal. `paystack` and
`paylony` have neither.

**Money types are inconsistent within this one repo**: `int64` outbound
(`models/dto.go:122`), but `int` inbound on the webhook
(`models/webhookPayload.go:8`) and on the status response
(`models/requestModels.go:40`). On a 32-bit build `int` overflows at ~21 million minor
units; more importantly it silently differs from the ledger's type.

**Webhook signature verification is commented out.** `internal/usecase/webhook.go:20-26`:
```go
// if !util.ValidateSignature(event, event.Checksum, uc.config.Juice.JuiceTag) {
//     return &models.BaseResponse{ResponseCode: models.UNAUTHORIZED_ERROR_CODE, ...}, err
// }
```
The verifier exists and looks correct — HMAC-SHA256 over stable-sorted JSON with
`subtle.ConstantTimeCompare`, `internal/util/token.go:174-188`. The webhook route has no
auth middleware either (`internal/controller/http/v1/juiceWebhook.go:12-19`), so
**anything on the internet can currently post a payment-succeeded event that moves money.**

**Fee handling can produce a negative leg.**
`internal/usecase/webapi/reserveFunds.go:62`:
```go
Amount: request.Amount - int64(api.config.App.Fee) - int64(api.config.App.Revenue),
```
No floor check. A transfer smaller than fee+revenue yields a negative credit leg.

**`internal/usecase/worker/inboundTransaction.go` is entirely commented out** — this
service cannot process collections at all today.

---

## Tasks

### 1. Adopt `swiftbon.shared`; make currency real

Bump `go.mod` to 1.24.1, add the `replace` directive.

**Currency comes from the account, never from config.** Delete
`config.App.DefaultCurrency` and every read of it. The flow is:

```
account_number → account.service SearchCustomerByAccountNumber → currency
```

`GetAccount` (`internal/usecase/webapi/searchCustomer.go`) already fetches the account;
prompt 02 adds `currency` to that response. Use it. A payout's source currency is a
property of the account being debited — it is not configurable.

Normalise every money field in this repo to `money.Money`:
`InitiateOutboundDto.Amount`, `SourceAmount`/`DestinationAmount`,
`WebhookPayload.Data.Amount` (currently `int`),
`GetTransactionStatusResponse.Data.Amount` (currently `int`), and the DB columns.

Existing `currency VARCHAR(100)` / `VARCHAR(10)` columns (`internal/entity/sql/schema/juice.sql:23,42,43,88`)
become `CHAR(3)` with a check constraint, backfilled to `NGN`.

### 2. Real FX payouts

This is the capability this service uniquely enables.

**Same-currency payout** (source account currency == destination currency): unchanged
flow, but set `SourceCurrency` and `DestinationCurrency` from the account and the
beneficiary rather than from config.

**Cross-currency payout:**

1. Request a quote from `fx.service` for (source account currency → destination currency).
2. Call postings `PostTransaction` with `fx_quote_id`. Postings produces the four-leg
   conversion and debits the source account. **This service does not compute the
   conversion itself** — a second conversion computed here would disagree with the
   ledger.
3. Send the provider the destination amount from the quote, with
   `SourceCurrency`/`DestinationCurrency` set correctly.
4. Persist `fx_quote_id` and the applied rate on the outbound transaction row so a
   customer query can show the rate.

Validate against the provider's supported corridors before quoting — do not quote a pair
the provider cannot settle. Add a `supported_corridors` config or, better, derive it from
the provider's bank-list/payment-methods response and cache it.

### 3. Fix the webhook security hole

**Uncomment and enable signature verification.** This is not optional cleanup — the
endpoint currently accepts unauthenticated events that trigger ledger reversals.

- Enable `util.ValidateSignature` in `internal/usecase/webhook.go`.
- Reject with `11` on mismatch. Log the rejection with the source IP.
- Add tests: valid signature accepted, tampered payload rejected, missing checksum
  rejected, replayed-but-valid handled idempotently.
- Verify the signing key comes from the secret store, not `config.yml` (which currently
  holds `tag: swiftbon1744639224641929946` and `pin: 123g` in plaintext).

**Add ingest idempotency.** `ProcessWebhook` currently inserts every payload into
`events` unconditionally and returns 200. Add a unique constraint on the provider's event
ID and treat a duplicate as success. At-least-once webhooks plus at-least-once seahorse
delivery means a money event can arrive twice; the dedupe must be at ingest.

### 4. Fee floor and revenue split

Guard `reserveFunds.go:62`:

```go
if request.Amount <= fee + revenue {
    return validation error — transfer below minimum
}
```

Fees are per currency. `config.yml` has `fee: 570` and `revenue: 430` (kobo, ₦5.70/₦4.30);
a flat 570 applied to a CAD transfer is $5.70. Move fees to a per-currency
configuration table or config map, keyed by currency and corridor, and reject a currency
with no configured fee rather than defaulting.

Fee and revenue legs must post to `REVENUE_<CCY>` (prompt 03 §5), not to a single
NGN revenue account.

### 5. Enable collections

Uncomment and complete `internal/usecase/worker/inboundTransaction.go`. Use `paylony`'s
version as a structural reference but **do not copy its money handling** — it has a float
round-trip bug (`ParseFloat(s, 10)` with an invalid bitSize, then `int64(f*100)` which
truncates `1.15*100 = 114.999…` to `114`). Parse the provider's amount into
`money.Money` using the currency the provider reports, validate that currency is
supported, and credit `COLLECTION_<CCY>` → customer account in the reported currency.

If the provider reports a currency that does not match the destination account's
currency, do **not** silently convert. Route to `SUSPENSE_<CCY>` and raise an
investigation event. Silent conversion on an inbound payment is how reconciliation
breaks.

### 6. Defects to fix in passing

- `md.Get("x-client-id")[0]` panics on metadata without that key.
- `internal/util/httpHandler.go` uses `&http.Client{}` with **no timeout** and no retry.
  A hung provider connection blocks a worker indefinitely. Set a timeout; add bounded
  retry with backoff for idempotent calls only.
- `httpHandler.go:86` returns `"http get request failed"` for POST errors too.
- `internal/util/token.go:49` has `var jwtSecretKey = []byte("AnyString")` as a fallback,
  and logs "Setting JWT secret key…" at **Error** level on the success path.
- `CreateWebhookJobs` (`worker/webhookJob.go:19-85`) has `return nil` paths after the
  marking loop that leave events stuck in `EventProcessing`.
- Unknown webhook events are marked `EventUnhandled` with `RetryCount+1`, so they churn
  through all 10 retries. Make unhandled terminal.
- `config.go` requires `merchant_key` and `tribe_account_ref` (`env-required:"true"`)
  inherited from bani; they are unused. Remove them.

---

## Verification

```bash
cd /Users/princeokechukwu/source/repos/swiftbon/swiftbon.juice.service
make compose-up && make migrate-up && make test   # -race
```

Against a provider sandbox, with account/postings/fx running:

- Same-currency CAD payout from a CAD account: correct currency reaches the provider,
  ledger debits the CAD account, fee posts to `REVENUE_CAD`.
- Cross-currency CAD→USD payout: quote obtained, postings produces four balanced legs,
  provider receives USD destination amount matching the quote, `fx_quote_id` and rate
  persisted.
- Attempt a payout from a CAD account with a stale FX rate: rejected cleanly, **no
  ledger movement**.
- Transfer of 100 minor units with fee+revenue of 1000: rejected with a validation error,
  no negative leg.
- Post an unsigned webhook: rejected with `11`.
- Post a validly-signed webhook twice: processed once.
- Inbound collection in USD to a USD account: credited. In EUR to a USD account: routed
  to suspense with an investigation event, **not** converted.
- Provider payout failure: reversal fires and the ledger returns to its prior state.

## Acceptance criteria

- [ ] Source currency derives from the account, never from config;
      `DefaultCurrency` is gone.
- [ ] `SourceCurrency` and `DestinationCurrency` can differ.
- [ ] FX payouts use an `fx.service` quote and postings' conversion — no local
      conversion arithmetic anywhere in this repo.
- [ ] Webhook signature verification is enabled, tested, and rejects tampered payloads.
- [ ] Webhook ingest is idempotent on the provider event ID.
- [ ] Fee floor enforced; per-currency fees; no negative legs possible.
- [ ] Collections work, and a currency mismatch goes to suspense rather than being
      converted.
- [ ] All money fields are `money.Money`; no `int` amounts remain.
- [ ] HTTP client has a timeout.
- [ ] Unit tests cover currency resolution, the FX path, signature verification, the fee
      floor, and the mismatch-to-suspense rule.

## Out of scope

- Rate sourcing (`15`), ledger mechanics (`03`).
- Building the shared provider abstraction. It is badly needed — four bespoke
  interfaces with incompatible DTOs is why adding a provider means forking a repo — but
  it is a refactor with its own risk profile. Note it; do not fold it in here.

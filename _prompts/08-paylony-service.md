# 08 — `swiftbon.paylony.service` — currency-aware NGN rail + money-precision bugs

> Read `00-READ-FIRST-shared-context.md` first. Requires `01`, `02`, `03`, `04`.
> Follow `05-juice-service.md` as the reference implementation and
> `07-paystack-service.md` for the reversal pattern — this repo needs both.

---

## Prompt

You are working in
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.paylony.service`
(module `swiftbon.com/paylony`, Go 1.25.2).

Swiftbon is a Canadian licensed MSB. This service wraps **Paylony**
(`https://api.paylony.com`) for Nigerian virtual accounts, payouts, and collections.

Like Paystack, this is an NGN-only rail and will stay that way. But **this repo has the
worst money handling in the estate** — two verified precision bugs that silently lose
customer funds, on both the payout and the collection path. Those are the priority, not
the currency work.

### Current state (verified)

**Two money bugs, both live.**

*Payout truncates.* `internal/usecase/worker/outboundTransaction.go:18`:
```go
Amount: fmt.Sprintf("%d", transaction.Amount/100), // convert kobo to naira
```
Integer division. A payout of ₦105.24 (`10524` kobo) is sent to the provider as `105`.
**24 kobo is silently destroyed on every payout with a non-zero remainder** — the ledger
was debited the full amount.

*Collection round-trips through a float.*
`internal/usecase/worker/inboundTransaction.go:40-46`:
```go
// TODO: check the amount here if it's naira or kobo to know if any convertion is needed
floatAmount, err := strconv.ParseFloat(webhookEvent.Amount, 10)
...
amount := int64(floatAmount * 100)
```
Three defects in six lines: `ParseFloat`'s second argument is *bitSize* and `10` is
invalid (valid values are 32 and 64 — Go treats out-of-range as 64, so this works by
accident); `int64(f * 100)` **truncates** rather than rounds, so `1.15 * 100` evaluates
to `114.999…` and becomes `114`; and the `TODO` admits nobody knows whether the
provider's amount is in naira or kobo. A collection can credit a customer one kobo short,
or by a factor of 100.

**Uniquely, this service sends amounts to the provider as a string.**
`internal/models/dto.go:140` — `InitiateOutboundDto.Amount string`. The other three send
`int64` (and `bani` sends `float64`). Four services, four representations of an amount.

**Currency is hardcoded in two places.**
- `internal/usecase/nameEnquiry.go:37` — `Currency: pgtype.Text{String: "NGN", Valid: true}`
- `internal/usecase/nameEnquiry.go:53` — `Currency: "NGN"`
- plus `config/config.yml:6` — `default_currency: NGN`, `env-required:"false"`.

**Provider endpoints:**

| Function | File | Call |
|---|---|---|
| `CreateCustomer` | `webapi/createCustomer.go:13` | `POST /api/v1/create_account` |
| `GetPayoutBankList` | `webapi/getBankList.go:13` | `GET /api/v1/bank_list` |
| `DoNameEnquiry` | `webapi/createRecipient.go:13` | `POST /api/v1/account_name` |
| `InitateOutbound` | `webapi/initiateOutbound.go:20` | `POST /api/v1/bank_transfer` |

**Account creation is wired oddly.** The consumer listens on
`means-of-identification-verified` (`config/config.yml:73`) rather than
`account-created` like `juice` and `paystack`, resolves the customer from the account
service by phone number, and persists locally — but **never calls the provider's
`create_account` endpoint**. So `webapi/createCustomer.go` is effectively dead: virtual
accounts are not actually being created. Confirm this before changing it; it may be
intentional or it may be an unfinished migration.

**Webhook signature verification is entirely absent.**
`internal/usecase/webhook.go:15-37` does no verification. The webhook route group has no
auth middleware. There *is* an HMAC-SHA512 helper
(`internal/usecase/webapi/paylonyUtilities.go:23-25`) but it is used only for signing
*outbound* requests — and **line 24 logs the signing key at Debug level.**

**Reserved funds can be stranded.** Same as Paystack:
`internal/usecase/worker/outboundTransaction.go:65` has
`//TODO: publish a failed transaction event`, and `processWebhook.go` has
`//TODO: fire event to reverse funds` twice (lines 138 and 163). No reversal path, no
status polling.

**A duplicate-detection bug.** `internal/usecase/worker/inboundTransaction.go:29-34`
returns the (nil) `err` on the duplicate branch, so a duplicate returns `nil` and the
event is marked processed by accident of the nil error rather than by intent. It happens
to be the right outcome for the wrong reason — make it explicit.

**Stamp duty is commented out** on the inbound path (`inboundTransaction.go:79-85`).

**Inbound status check uses a magic string:** `webhookEvent.Status != "00"`.

---

## Tasks

### 1. Fix the two money bugs first

Do this before the currency work, in its own commit, with its own tests. These are
independent of multi-currency and they are losing money now.

**Payout.** The ledger's unit is minor units (kobo). Determine what unit Paylony's
`bank_transfer` endpoint actually expects — read their documentation, do not infer it
from this code. Then:

- If the provider wants naira: convert explicitly and **reject any amount with a non-zero
  kobo remainder** rather than truncating, or handle the remainder deliberately. Silently
  dropping it is not an option.
- If the provider wants kobo: send `transaction.Amount` unchanged.

Either way, replace the `/100` with `money.Money` and an explicit, tested conversion.
Add a test with `10524` asserting the exact value sent.

**Collection.** Delete the float path entirely. Parse the provider's amount as an exact
decimal (`shopspring/decimal`, already a dependency of `swiftbon.shared`), determine the
unit from the provider's documented contract, and convert to `money.Money` with
`round_half_up` — never `int64(f * 100)`.

Resolve the `TODO`. If the unit genuinely cannot be determined from documentation, do not
guess: reject the webhook, alert, and get it confirmed against a real sandbox payment.
Crediting a customer 100× or 1/100× the correct amount is worse than a failed webhook.

Add a test with `"1.15"` asserting `115`, and with `"105.24"` asserting `10524`.

**Then audit the whole repo** for other `/100`, `*100`, `float`, and `ParseFloat` uses in
money paths and fix each one.

### 2. Adopt `swiftbon.shared`; normalise the amount type

Add the `replace` directive. Convert every money field to `money.Money`, including
`InitiateOutboundDto.Amount` (currently `string`) and `WebhookEvent.Amount` (currently
`string`). Serialise to the provider's required wire format at the adapter boundary
only — the string representation is a provider quirk and must not leak inward.

### 3. Make the currency explicit

Replace `default_currency` with:
```yaml
paylony:
  settlement_currencies: ['NGN']
```

Remove the two hardcoded `"NGN"` literals in `nameEnquiry.go` and derive from the
configured settlement currency.

Validate at every entry point:
- payout from an account whose currency is not NGN → reject **before** ledger movement
- inbound collection not in NGN → route to `SUSPENSE_NGN` with an investigation event,
  never convert

Currency columns become `CHAR(3)` with a check constraint, backfilled to `NGN`. Note they
are currently `VARCHAR(100)` in places (`internal/entity/sql/schema/paylony.sql:23,45,86`)
— a 100-character currency column is a sign nobody validated the value.

**Do not add FX here.** Conversion happens in the ledger.

### 4. Reversal, polling, reconciliation

Port from `juice` (`worker/reverseFailedTransaction.go`,
`worker/getPendingTransactionStatus.go`) exactly as prompt 07 describes for Paystack:

- reverse the ledger reservation on every terminal failure, idempotently
- poll for transactions stuck pending past a threshold
- escalate rather than auto-reverse when a payout's outcome is genuinely unknown
- add a reconciliation cron surfacing ledger/provider disagreements

Replace both `//TODO`s with implementations.

### 5. Webhook security

Implement Paylony's documented signature verification over the raw body with
`subtle.ConstantTimeCompare`. Reject with `11`. Add ingest idempotency on the provider's
event ID via a unique constraint.

**Remove the key logging** at `paylonyUtilities.go:24` — it writes the signing key to the
log file, which is shipped to Loki.

Replace the `Status != "00"` magic string with a named constant or enum.

Make the duplicate branch in `inboundTransaction.go:29-34` return an explicit
already-processed result rather than a nil error.

### 6. Resolve the account-creation gap

Determine whether virtual accounts are meant to be created here. If yes, wire
`webapi/createCustomer.go` into the consumer path and align the trigger topic with
`juice`/`paystack` (`account-created`, which prompt 02 makes carry currency). If no,
delete the dead code and document why.

Do not leave it ambiguous — a payment rail where account creation may or may not happen
is not something to carry forward.

### 7. Defects to fix in passing

- `md.Get("x-client-id")[0]` panics on metadata without that key.
- `internal/util/httpHander.go` [sic] — `&http.Client{}` with no timeout. Fix and rename.
- Unknown webhook events churn through all 10 retries. Make unhandled terminal.
- Stamp duty commented out on inbound — with prompt 03's `levy_rules`, decide explicitly
  whether NGN collections attract it and implement or delete.
- `internal/util/token.go` — `jwtSecretKey = []byte("AnyString")` fallback; logs at Error
  level on success.
- README is stale boilerplate.
- `config/config.yml:80-82` contains live-looking `sk_test_…`, `pk_test_…`, and
  `encryption_key`; `.env.example` contains an `sk_live_…` token. Raise separately;
  rotate.
- CI pins Go 1.17 against a `go 1.25.2` module.

---

## Verification

```bash
cd /Users/princeokechukwu/source/repos/swiftbon/swiftbon.paylony.service
make compose-up && make migrate-up && make test   # -race
```

**Money precision — the tests that matter most:**

- Payout of `10524` minor units: assert the exact value sent to the provider. On the
  current code this sends `105`. **This is the regression test for task 1.**
- Collection of `"1.15"`: assert `115` credited. Current code credits `114`.
- Collection of `"105.24"`: assert `10524`.
- Property test: for a range of amounts, ledger debit == provider amount == ledger
  reversal amount, with no drift.

**Then:**

- NGN payout from an NGN account: unchanged behaviour. Regression test.
- Payout from a CAD account: rejected before any ledger movement; CAD balance untouched.
- Provider failure after reservation: reversal returns the ledger to its prior state.
- Unsigned webhook: rejected with `11`.
- Valid webhook twice: processed once.
- Non-NGN collection: suspense, not converted.
- Grep the log output of a full payout run for the signing key — it must not appear.

## Acceptance criteria

- [ ] No integer-truncating division and no float in any money path; both bugs fixed with
      tests asserting exact values.
- [ ] The naira/kobo unit question is resolved against provider documentation, not
      guessed, and the `TODO` is gone.
- [ ] All money fields are `money.Money`; the provider's string format exists only at the
      adapter boundary.
- [ ] `settlement_currencies` explicit; both hardcoded `"NGN"` literals gone.
- [ ] Non-NGN payout rejected before ledger movement; non-NGN collection to suspense.
- [ ] Every failure path reverses idempotently; both `//TODO`s gone; polling and
      reconciliation exist.
- [ ] Webhook signature-verified; ingest idempotent.
- [ ] Signing key no longer logged.
- [ ] Account-creation path is either wired up or deliberately removed, with the decision
      documented.

## Out of scope

- Making Paylony settle non-NGN currencies.
- FX conversion.
- The shared provider abstraction.

# 12 — `swiftbon.communication.server` — currency-correct alerts

> Read `00-READ-FIRST-shared-context.md` first. Requires `01`, `03`, `04`.

---

## Prompt

You are working in
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.communication.server`
(module `swiftbon.com/communication`, Go 1.24.1).

Swiftbon is a Canadian licensed MSB. This service is the outbound notification hub — SMS
via Twilio, email via SMTP, and templated WhatsApp alerts posted to the chatbot service.

The change here is small in code and highly visible to customers: **this service currently
prints `₦` on every transaction alert, so a CAD transaction is reported to the customer as
naira.**

### Current state (verified)

**The naira symbol and the ÷100 scale are hardcoded.**
`internal/usecase/consumers/handlePostingsAlerts.go:16-56`, on `transaction-completed`:

```go
Amount:  fmt.Sprintf("₦%s", humanize.CommafWithDigits(float64(event.AllowedAmount)/100, 2)),
Balance: fmt.Sprintf("₦%s", humanize.CommafWithDigits(float64(event.ClosingBalance)/100, 2)),
```

Three problems in two lines: the symbol is a literal, the `/100` assumes exponent 2, and
the value is cast to `float64` for formatting — which for a large balance loses precision
in the displayed figure.

**The event it consumes has no currency.**
`swiftbon.postings.service/internal/models/eventModels.go` —
`TransactionEventModel` carries `OpeningBalance`, `ClosingBalance`, `LedgerBalancer`
[sic], `Amount`, `AllowedAmount`, `LienAmount`, all `int64`, with no currency field.
**Prompt 03 adds `currency` and `schema_version: 2`.** This service cannot be correct
until that lands.

**Alert targets** (`config/config.yml:60-67`) at `https://bot.swiftbon.com`:
`/api/v1/sys-msg/credit_alert`, `/debit_alert`, `/kyc_bvn`, `/kyc_nin`, `/kyc_failure`,
authenticated with an `x-client-id` header (`send_debit_notification.go:14`).

> Note: those `sys-msg` endpoints are **not present** in
> `swiftbon.chatbot.whatsapp.service`. This integration is broken or unbuilt on the
> receiving side. Confirm before assuming alerts are being delivered today — see
> `13-chatbot-whatsapp-service.md`.

**Topics consumed** (`config/config.yml:41-53`, ttl 525600 minutes): `send-sms`,
`send-email`, `transaction-completed`, `bvn-verified`,
`means-of-identification-verified`, `id-verification-failed`.

**A dead switch branch:** `IDVerificationFailed` appears twice in the dispatch switch
(`internal/controller/pubsub/seahorse/consumer.go:80` and `:120`); the second is
unreachable.

**Two binaries:** `cmd/app` (HTTP 8084, gRPC 8094) and `cmd/consumer` (gRPC 8204).

---

## Tasks

### 1. Adopt `swiftbon.shared`

Add the `replace` directive. Use `money.Money` for every amount in an alert payload and
`money.Format` for display.

### 2. Currency-correct formatting

Replace the hardcoded formatting with a currency-aware formatter driven by the registry's
exponent — not a hardcoded `/100`:

```go
// display only; never parsed back
func FormatForDisplay(m money.Money, locale string) string
```

Requirements:

- Symbol and placement from the currency, not a literal: `CA$1,234.56`, `$1,234.56`,
  `₦1,234.56`, `£1,234.56`, `€1,234.56`.
- Scale from `money.Lookup(code).Exponent`, so a future exponent-0 currency does not get
  a spurious `.00`.
- **No `float64` in the formatting path.** Format the integer minor units directly —
  split on the exponent and insert the separator. `humanize.CommafWithDigits(float64(x)/100, 2)`
  is lossy for large balances and there is no reason to go through a float to insert
  commas.
- Disambiguate `$`: `CA$` for CAD and `US$` for USD when both are in play. A Canadian MSB
  customer holding both must never be shown a bare `$`.

Where CAD and USD are ambiguous in a short SMS, prefer the ISO code — `1,234.56 CAD` is
unambiguous and short. Pick one convention and apply it consistently.

### 3. Read currency from the event

Consume `currency` from `transaction-completed`. Handle the rollout window explicitly per
prompt 04 §5:

- `schema_version` ≥ 2 → use the event's currency.
- `schema_version` absent, `0`, or `1` → treat as NGN **and log a warning with the event
  ID**, so the tail of legacy events is visible and step 4 of the rollout runbook can be
  verified.

Never default silently. A CAD transaction formatted as naira is a customer-trust problem
and a support cost, and a silent default is exactly how it would go unnoticed.

### 4. Per-currency and per-locale templates

A Canadian customer should not receive a naira-formatted alert, and a Nigerian customer
should keep receiving exactly what they receive today.

- Add `locale` (or at minimum `country`) to the customer record read for alerting. The
  account service holds `country` (prompt 02 makes it required); fetch it via the existing
  gRPC client — this repo already has `pkg/grpcserver/clients/account.proto` and an
  `ACCOUNT_SERVER` config that the consumer paths do not currently use.
- Template selection keyed by (alert type, locale), falling back to English.
- Keep the existing Nigerian templates byte-identical for NG customers. **This is a
  regression requirement**, not a nice-to-have.

### 5. Multi-currency balance in alerts

A customer with CAD, USD, and NGN accounts receiving "your balance is 1,234.56" needs to
know *which* balance. Include the account's currency in every balance line, and where the
template shows a balance, show it for the account the transaction touched — not an
aggregate. Do not sum balances across currencies anywhere; there is no meaningful total
without a rate, and inventing one in a notification would be wrong.

### 6. Defects to fix in passing

- `md.Get("x-client-id")[0]` panics on metadata without that key.
- Remove the duplicate unreachable `IDVerificationFailed` switch branch.
- SMS and email sends are fire-and-forget goroutines with no error propagation
  (`internal/usecase/sms.go:27`, `email.go:26`) — a failed alert is invisible. Persist a
  send status and retry. For a money alert, silent failure is a real gap.
- `internal/usecase/sms.go:65-74` checks `err != nil` on a freshly-declared nil `err` —
  dead branch.
- `email.go:50` writes `request.Subject` into the `Body` column.
- `pkg/logger/logger.go` hardcodes `log.SetLevel(log.TraceLevel)`, ignoring `cfg.Level`.
- `internal/middleware/trackResponse.go:15` logs
  `microservice: "persian.black.devtroy.auth.service"`.
- Auth here uses a `Client_Id` header while the rest of the estate uses `x-client-id`.
  Support both, then converge.
- `config/config.yml:22-33` contains live-looking Twilio credentials and the SMTP password
  `Princess4Daprinz`. Raise separately; rotate.
- README is stale boilerplate.
- CI pins Go 1.17 against a `go 1.24.1` module.

---

## Verification

```bash
cd /Users/princeokechukwu/source/repos/swiftbon/swiftbon.communication.server
make compose-up && make migrate-up && make test   # -race
```

Unit tests on the formatter first — this is cheap and catches the whole class of bug:

| Input | Expected |
|---|---|
| `{123456, "CAD"}` | `CA$1,234.56` |
| `{123456, "USD"}` | `US$1,234.56` |
| `{123456, "NGN"}` | `₦1,234.56` |
| `{123456, "GBP"}` | `£1,234.56` |
| `{123456, "EUR"}` | `€1,234.56` |
| `{5, "CAD"}` | `CA$0.05` |
| `{0, "CAD"}` | `CA$0.00` |
| `{-123456, "CAD"}` | `-CA$1,234.56` |
| `{922337203685477580, "CAD"}` | exact, no float precision loss |

Then end-to-end with postings running:

- CAD debit → alert shows CAD, not naira.
- NGN debit → alert byte-identical to today's. **Regression test.**
- `schema_version: 1` event → formatted as NGN with a warning logged naming the event ID.
- Nigerian customer → existing Nigerian template unchanged.
- Canadian customer → English/CAD template.
- Twilio failure → send status recorded and retried, not silently dropped.

## Acceptance criteria

- [ ] Alerts show the correct currency symbol and scale for all five currencies.
- [ ] No `₦` literal and no `/100` remains in a formatting path.
- [ ] No `float64` in the formatting path; large amounts format exactly.
- [ ] CAD and USD are never shown as a bare `$`.
- [ ] Currency read from the event; legacy events treated as NGN **with a warning**, never
      silently.
- [ ] Nigerian customers' alerts are unchanged.
- [ ] Balances always carry their currency; no cross-currency summing anywhere.
- [ ] Send failures are recorded and retried.
- [ ] Formatter has full unit-test coverage including negative, zero, and large values.

## Out of scope

- Adding the event's currency field — that is prompt 03.
- Building the chatbot's `sys-msg` receiving endpoints — that is prompt 13. Note the
  broken integration in the PR.
- Rotating committed credentials (raise separately).

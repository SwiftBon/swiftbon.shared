# 09 — `swiftbon.bani.service` — decide its future, then make it currency-explicit

> Read `00-READ-FIRST-shared-context.md` first. Requires `01`, `02`, `03`.

---

## Prompt

You are working in
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.bani.service`
(module `swiftbon.com/bani`, Go 1.22.2).

Swiftbon is a Canadian licensed MSB. This service wraps **Bani**
(`https://stage.getbani.com/api/v1`).

**Read this before doing anything else: this service is barely implemented, and the first
task is to decide whether it should exist.** Do not do multi-currency work on a service
that is about to be deleted.

### Current state (verified)

**It is the least developed of the five provider integrations.** No `cmd/worker`, no
`cmd/webhook`, no webhook router, no inbound or outbound transaction handling. Its
`internal/usecase/interfaces.go` exposes a **single** method (`BaniMs`), against 5 each
for `PaylonyMs`, `PaystackMs`, and `JuiceMs`.

**Only two provider calls exist**, both read-only:
- `internal/usecase/webapi/getBankList.go:13` — `GET {host}/partner/list_banks/ng/`
  (**`ng` hardcoded in the path**)
- `internal/usecase/webapi/nameEnquiry.go:13` —
  `POST {host}/partner/payout/verify_bank_account/`

**`InitateOutbound` is a copy-pasted Twilio SMS sender.**
`internal/usecase/webapi/initiateOutbound.go`:
```go
func (api *BaniWebApi) InitateOutbound(request *models.SendSmsRequest, fields log.Fields) error {
	accountSid := api.config.Twilio.SID
	...
	req, _ := http.NewRequest("POST", twilioSmsURL, &msgDataReader)
```
It takes an SMS request, sends an SMS, is named "initiate outbound", is not in the
`BaniWebApi` interface, and is referenced by nothing. This is the clearest signal of the
repo's state.

**The routes are leftovers from the communication-service template.**
`internal/controller/http/v1/bani.go:17-27`:
```
POST /v1/api/sms      POST /v1/api/email
GET  /v1/api/email    GET  /v1/api/banklist
```
An SMS and an email endpoint on a payments service.

**It points at a staging host.** `config/config.yml:48-53` —
`https://stage.getbani.com/api/v1`. There is no production configuration.

**Currency appears only in a doc comment.**
`internal/models/requestModels.go:3-24` documents a Bani virtual-account payin payload
with `"country_code":"NG"` and `"pay_currency":"NGN"`, and declares
`Currency string \`json:"pay_currency"\`` at line 19 — **nothing populates it.**

**It sends amounts as `float64`.** `internal/models/requestModels.go:18` —
`Amount float64 \`json:"pay_amount"\``. This is the only service in the estate using a
float for money. Four services, four representations: `int64` (juice, paystack),
`string` (paylony), `float64` (bani).

Bani is a Nigerian provider. `list_banks/ng/` is the only corridor wired.

---

## Task 0 — decide (do this first, and get an answer before proceeding)

Ask the owner one question: **is Bani a rail Swiftbon intends to use?**

**If no** — archive the repo. Do not spend effort on it. Note in the PR that
`nameEnquiry` and `getBankList` are the only live functionality and that neither is
referenced from a money path, so removal is low-risk. Confirm that by grepping the other
repos for calls into this service before deleting anything.

**If yes** — it needs to be built, not adjusted. It has no payout path, no webhook
handling, no reversal, and no worker. That is a build-a-service task, and the currency
work is a small part of it. Proceed with tasks 1–4 below and scope them honestly: this
is not a multi-currency change, it is completing an integration.

**If undecided** — do task 1 only. It is cheap, it removes the actively misleading code,
and it leaves the repo in a state where the decision can be made later without confusion.

---

## Task 1 — remove what is actively misleading (do this regardless)

Low risk, high clarity, and safe even if the service is later deleted.

- **Delete `internal/usecase/webapi/initiateOutbound.go`.** It is a Twilio SMS function
  masquerading as a payout function in a payments service. Someone will eventually call
  it. Verify it is unreferenced first (it is, but check).
- **Delete the `/v1/api/sms` and `/v1/api/email` routes** and their handlers. This is not
  a communication service.
- **Replace the README.** It is stale boilerplate titled "Persian Black AccountMs
  Service" describing an SMS/email service.
- **Document the actual state** in the README: two read-only provider calls, no payout
  path, staging host only, not wired into any money flow.
- **Remove the unused `Twilio` and `Smtp` config blocks** once the SMS/email code is gone.
- Fix `md.Get("x-client-id")[0]`, which panics on metadata without that key.
- Set a timeout on `&http.Client{}` in `internal/util/httpHander.go` [sic — also rename
  the file].

Do **not** rotate the committed Bani access token (`config/config.yml:53`) inside this PR
— raise it separately, but do raise it.

---

## Tasks 2–4 — only if the answer to task 0 is "yes"

### 2. Adopt `swiftbon.shared`; eliminate the float

Bump `go.mod` from 1.22.2 to 1.24.1 and add the `replace` directive.

**Replace `Amount float64` with `money.Money`.** A float amount cannot represent minor
units exactly — `0.1 + 0.2 != 0.3` — and this is the only place in the estate that uses
one for money. Convert at the provider boundary if Bani's API genuinely requires a
decimal string, but never hold a float internally.

Populate the `Currency` field that is currently declared and unused, from the account's
currency (prompt 02 adds `currency` to the account service response) — never from config.

### 3. Make the currency and corridor explicit

```yaml
bani:
  settlement_currencies: ['NGN']
  supported_countries: ['NG']
```

Derive the `ng` in `list_banks/ng/` from `supported_countries` rather than hardcoding it,
so adding a corridor is configuration rather than a code change — and so it is visible
that only one corridor is supported.

Validate at entry: a payout from a non-NGN account is rejected before any ledger
movement. An inbound payment not in NGN goes to `SUSPENSE_NGN` with an investigation
event, never converted.

**No FX here.** Conversion happens in the ledger via `fx.service` quotes.

### 4. Build the missing integration

This is the real work, and it is not currency work. Follow
`swiftbon.juice.service` as the reference — it is the most complete of the five and has
the pieces this repo lacks:

- `cmd/worker` and `cmd/webhook` binaries, plus `Dockerfile.consumer`-style build targets
- a webhook router on its own port, with **signature verification implemented from the
  start** (all three services that have webhooks today ship with verification either
  absent or commented out — do not add a fourth)
- webhook ingest idempotency on the provider event ID, enforced by a unique constraint
- the payout flow: reserve funds via postings `PostTransaction` → call provider → resolve
  by webhook or polling
- **reversal on every terminal failure**, idempotent. `paystack` and `paylony` both ship
  with `//TODO: fire event to reverse funds` and strand customer funds as a result. Build
  the reversal first, not last.
- status polling for transactions stuck pending
- a reconciliation cron for ledger/provider disagreements
- migrations for the outbound and inbound transaction tables, with `currency CHAR(3)`
  from the start rather than backfilled
- fee and revenue keyed by currency, with a floor check so a small transfer cannot
  produce a negative credit leg (the bug present at
  `juice/internal/usecase/webapi/reserveFunds.go:62`)
- a **production** provider host configuration

Scope this honestly to the owner: it is a multi-week integration, not a currency patch.

---

## Verification

**After task 1 (always):**
```bash
cd /Users/princeokechukwu/source/repos/swiftbon/swiftbon.bani.service
go build ./... && make test
grep -rn "InitateOutbound\|twilioSmsURL" --include='*.go' .   # expect no results
grep -rn "/v1/api/sms\|/v1/api/email" --include='*.go' .      # expect no results
```
Confirm nothing else in the estate calls this service:
```bash
cd /Users/princeokechukwu/source/repos/swiftbon
grep -rn "bani" --include='*.go' --include='*.yml' . | grep -v "swiftbon.bani.service/"
```

**After tasks 2–4 (if built):** the same verification set as
`07-paystack-service.md` — regression on NGN payout, rejection of a non-NGN payout with
the balance untouched, reversal on provider failure, polling resolution, unsigned webhook
rejected, duplicate webhook processed once, non-NGN collection to suspense.

## Acceptance criteria

**Task 1 (always):**
- [ ] The Twilio SMS function named `InitateOutbound` is deleted.
- [ ] SMS and email routes and handlers are gone; Twilio/SMTP config removed.
- [ ] README describes what the service actually is and its limitations.
- [ ] `x-client-id` panic fixed; HTTP client has a timeout.

**Tasks 2–4 (only if the service is being kept):**
- [ ] No `float64` remains in any money path.
- [ ] Currency derives from the account; the declared `Currency` field is populated.
- [ ] `settlement_currencies` and `supported_countries` explicit; `ng` no longer
      hardcoded in a URL path.
- [ ] Non-NGN payout rejected before ledger movement; non-NGN collection to suspense.
- [ ] Payout, webhook, polling, reversal, and reconciliation all exist.
- [ ] Webhook signature verification present and tested — not commented out.
- [ ] A production provider host is configured.

## Out of scope

- Multi-currency settlement through Bani. It is a Nigerian provider.
- FX conversion.
- Rotating the committed access token (raise separately).

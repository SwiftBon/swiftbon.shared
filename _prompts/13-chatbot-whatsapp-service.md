# 13 — `swiftbon.chatbot.whatsapp.service` — currency selection and display

> Read `00-READ-FIRST-shared-context.md` first. Requires `01`, `02`, `03`, `12`, `15`.

---

## Prompt

You are working in
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.chatbot.whatsapp.service`
(module `swiftbon.com/chatbot/whatsapp`, Go 1.22.2 — needs a bump to 1.24.1).

Swiftbon is a Canadian licensed MSB. This is the WhatsApp Cloud API bot: conversational
onboarding, KYC collection, and a menu-driven banking interface, driven by a declarative
YAML state machine (`config/flow.yml`).

For most customers this bot **is** Swiftbon. Multi-currency accounts are invisible unless
this surface exposes them.

### Current state (verified)

**No currency handling at all.** Zero references anywhere in the repo.

**It does not move money.** Grepping `postings|reservefunds|debit|billers|payment` across
`internal/` yields only an unrelated enum and a log field.
`internal/usecase/interfaces.go:14-17` exposes only `SaveBotEvent` and `SaveFlowEvent`.
The menu offers **Send Money, Pay Bills, Buy Airtime, Check Account Balance, Manage
Account, Pay Swiftbon Business** — and **none of those handlers are implemented.**

So the money-moving parts of this bot do not exist yet. Scope accordingly: this is
mostly a build, not a currency retrofit.

**Three binaries and no `cmd/app`:** `cmd/webhook`, `cmd/worker`, `cmd/webhookWorker`.

**Routes** (`internal/controller/http/v1/chatbot.go:20-25`), guarded by
`VerifySignatureMiddleware` (Meta signature — good, and notably the only service in the
estate that verifies webhook signatures properly):
```
POST /v1/webhook/chat
POST /v1/webhook/flow
```
Both persist the payload and return; processing is async in the worker
(`internal/usecase/worker/{handleBotEvents,handleFlowEvent,newUserHandler,existingUserHandler,collectBvnHandler,processUserBVNHandler,processUserIDHandler,processUserAddressHandler}.go`).

Encrypted WhatsApp Flows via `config/{private,public}.pem` and `internal/util/crypto.go`.
Tables: `user`, `session`, `chat_tracker`, `event`, `audit_log`.

**`config/flow.yml`** defines states, actions, conditions, and user-status branching:
1 onboarded, 2 no BVN, 3 no address, 4 BVN rejected, 5 ID rejected, 6 address rejected.
**Every one of those states is Nigeria-specific** — a Canadian customer has no BVN, so
status 2 and 4 are meaningless and the flow has no path for them.

**The repo is in poor shape:**

- `config/config.yml` is stale: `name: 'communication.service'`, Twilio and SMTP blocks
  that do not belong, a Twilio endpoint mangled to `.../Chatbots/...` at line 25, and
  **none of the `sys-msg` paths** the communication server posts to.
- `config/config.go:113` hardcodes `./config/config.yml` instead of reading
  `CONFIG_LOCATION`.
- **`.github/workflows/cd.yml` deploys this repo as `kyc`** — target `~/bin/kyc/`, unit
  `kyc-app.service`, `systemctl restart kyc-app` — and builds `./cmd/app`, **which does
  not exist**, so the job fails at line 33. This service is not currently deployable.
- **The `/api/v1/sys-msg/*` endpoints that `communication.server` posts alerts to are not
  present in this repo.** The alerting integration is broken on the receiving side.
- `.env.example` contains a WhatsApp permanent auth token, a Cloudinary secret, a MongoDB
  Atlas URI, and an encrypted RSA private key; `config/private.pem` is committed.

---

## Tasks

### 0. Fix deployability first

Nothing else can be verified while the service cannot deploy.

- Fix `cd.yml`: correct the deploy target from `kyc` to `chatbot`, correct the systemd
  unit name, and build the binaries that actually exist (`cmd/webhook`, `cmd/worker`,
  `cmd/webhookWorker`) rather than `cmd/app`.
- Clean `config/config.yml`: correct the service name, remove the Twilio and SMTP blocks,
  remove the mangled endpoint.
- Read `CONFIG_LOCATION` in `config.go` like every other service.
- Bump `go.mod` to 1.24.1.

Rotating the committed credentials is urgent but belongs in its own PR — raise it. The
committed `private.pem` and the WhatsApp permanent token are the highest-severity items in
this repo.

### 1. Adopt `swiftbon.shared`

Add the `replace` directive. Use `money.Money` for every amount and the shared display
formatter for every figure shown to a customer — reuse the one built in prompt 12 rather
than writing a second one. Two formatters will diverge, and a customer seeing `₦` in the
bot and `CA$` in the SMS for the same transaction is worse than either alone.

### 2. Add the `sys-msg` endpoints

Build the endpoints `communication.server` already posts to
(`swiftbon.communication.server/config/config.yml:60-67`):

```
POST /api/v1/sys-msg/credit_alert
POST /api/v1/sys-msg/debit_alert
POST /api/v1/sys-msg/kyc_bvn
POST /api/v1/sys-msg/kyc_nin
POST /api/v1/sys-msg/kyc_failure
```

Authenticate with the `x-client-id` header, matching what the sender uses. Accept the
currency-aware payload from prompt 12 and render it with the shared formatter — never
reformat the amount locally, and never assume a symbol.

Add `kyc_ca_id` (or a generic `kyc_id_verified`) for Canadian ID verification, since
`kyc_bvn` and `kyc_nin` are Nigeria-only.

### 3. Currency in the conversational flow

**Account opening.** `config/flow.yml` needs a currency-selection state. Fetch the list
from the account service's `GET /account/v1/api/currencies` (prompt 02) — **do not
hardcode the five currencies in the YAML**, or adding a currency means a bot deploy.

Present it as a WhatsApp list message. Default the highlighted option to the currency
matching the customer's country (CAD for CA, NGN for NG) but require an explicit
selection.

**Balance check.** "Check Account Balance" must show **all** accounts with their
currencies:
```
Your balances:
CA$1,234.56  (Chequing ···4567)
US$   410.00 (USD ···9876)
₦ 88,000.00  (NGN ···5566)
```
Never sum across currencies. There is no meaningful total without a rate, and inventing
one in a chat message would be presenting a fabricated figure as a balance.

**Send Money.** Add a source-account (and therefore currency) selection step when the
customer holds more than one. Skip it when they hold one. Ask before the amount, so the
amount is unambiguous.

**Cross-currency send.** When source and destination currencies differ:

1. Get a quote from `fx.service` via the backend.
2. Show the customer: amount sent, amount received, the rate, the fee, and **the expiry
   as a countdown** ("expires in 90 seconds").
3. Require an explicit confirmation.
4. On confirm, validate the quote is still live. If it expired, **re-quote and ask
   again** — never execute at a rate the customer did not see.

A chat interface makes expiry handling harder than a web form: the customer may reply
minutes later. Handle a stale confirmation explicitly and gracefully — re-quote and
re-present, do not error out or silently execute.

**Bill payments and airtime.** These go through `billers` (prompt 10), which returns a
funding-currency amount and a rate. Show both.

### 4. Jurisdiction-aware onboarding

The six user-status branches in `flow.yml` are Nigeria-shaped. Restructure so the
onboarding path is a function of the customer's country:

- Ask country early — it determines everything downstream.
- **NG:** existing BVN and NIN flow, unchanged. **Regression requirement.**
- **CA:** passport or provincial driver's licence, address, no BVN. Statuses 2 and 4 (no
  BVN / BVN rejected) must not be reachable for a Canadian customer.

Restructure `flow.yml` so country-specific states are separate branches rather than
conditions bolted onto a single path. Add validation that every terminal state is
reachable for every supported country — a Canadian customer stuck in a BVN state with no
exit is a dead-end conversation with no error message.

### 5. Implement the money-moving handlers

Send Money, Pay Bills, Buy Airtime, and Pay Swiftbon Business are menu items with no
implementation. Build them against the multi-currency backends:

- `postings` for transfers (via the single atomic primitive from prompt 03)
- `billers` for bills and airtime (prompt 10)
- `fx.service` quotes for cross-currency (prompt 15)

**Idempotency matters especially here.** WhatsApp delivers webhooks at least once, and a
customer can double-tap a confirmation button. Key every money operation on a
deterministic reference derived from the session and the confirmation event ID, so a
duplicate confirmation is a no-op rather than a second transfer. The estate's postings
service is made idempotent on `(instrument_number, reference_number)` in prompt 03 — use
it deliberately.

Never display a raw error to a customer. Map failures to plain language, and log the
detail.

### 6. Defects to fix in passing

- HTTP client timeouts on all outbound calls.
- `internal/util/crypto.go` — confirm the Flow decryption path handles malformed input
  without panicking; a panic in a webhook handler takes the process down.
- Confirm session state cannot be resumed across a currency change mid-flow (a customer
  switching accounts halfway through a send).
- README is stale boilerplate.

---

## Verification

```bash
cd /Users/princeokechukwu/source/repos/swiftbon/swiftbon.chatbot.whatsapp.service
go build ./... && make test   # -race
```

Confirm `cd.yml` builds and targets the right paths — a dry run, not a deploy.

Against the WhatsApp sandbox with the full backend running:

- Onboard a Canadian customer: country asked, passport path offered, **no BVN state
  reachable**, CAD account opened.
- Onboard a Nigerian customer: flow identical to today. **Regression test.**
- Check balance with CAD, USD, and NGN accounts: all three shown with correct symbols,
  **no total line**.
- Send CAD→CAD: source selection, amount, confirm, executes.
- Send CAD→USD: quote shown with rate, fee, received amount, and countdown; confirm within
  the window executes at the quoted rate.
- Send CAD→USD and confirm **after** expiry: re-quoted and re-presented, not executed at
  the old rate and not an error dead-end.
- Double-tap confirm: exactly one transfer.
- `POST /api/v1/sys-msg/credit_alert` with a CAD payload: renders `CA$`, not `₦`.
- Currency list reflects `GET /account/v1/api/currencies` — add a sixth currency to the
  account service and confirm it appears with **no bot deploy**.

## Acceptance criteria

- [ ] `cd.yml` deploys this service as chatbot, building binaries that exist.
- [ ] `config.yml` is clean; `CONFIG_LOCATION` honoured.
- [ ] Currency selection at account opening, fetched from the API, not hardcoded.
- [ ] Balance view shows every currency and never sums across them.
- [ ] Cross-currency send shows rate, fee, received amount, and a live expiry; an expired
      confirmation re-quotes rather than executing or failing.
- [ ] Money operations are idempotent against duplicate confirmations.
- [ ] Canadian onboarding works with no BVN state reachable.
- [ ] Nigerian onboarding unchanged.
- [ ] `sys-msg` endpoints exist and render with the shared formatter.
- [ ] All amounts use `money.Money` and the prompt-12 formatter — no second formatter.

## Out of scope

- Rotating the committed WhatsApp token, Cloudinary secret, Mongo URI, and `private.pem`
  (urgent — raise as its own PR).
- Redesigning the conversational UX beyond what currency requires.
- Voice or non-WhatsApp channels.

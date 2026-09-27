# 00 — Shared context (READ FIRST)

Every other prompt in this folder assumes this document. When you start a session from
any `NN-*.md` prompt, read this file first: it defines the money model, the currency
registry, the FX design, the ledger invariants, and the conventions that must be
identical across all 14 repos.

Repo root: `/Users/princeokechukwu/source/repos/swiftbon/`

---

## 1. What Swiftbon is, and what is changing

Swiftbon is a **Canadian licensed Money Services Business (MSB)**. The codebase it is
built on was written for a **Nigerian** retail-banking product and carries that
assumption everywhere — implicitly, not explicitly.

**The goal of this work:**

1. Open accounts for customers in **any supported currency**.
2. Process transactions for those customers, including **cross-currency (FX)**.

**Decisions already made** (do not relitigate these):

| Decision | Choice |
|---|---|
| Multi-currency model | **Account per currency.** A customer may hold N accounts, one per currency. `customer_accounts` gains a `currency` column. |
| Launch currencies | **CAD, USD, NGN, GBP, EUR** |
| FX | **In scope.** Cross-currency transfers convert via quoted rates and FX position accounts. |
| Ledger atomicity | **Fix it.** All money movement goes through one transactional double-entry primitive. |

---

## 2. The single most important fact about the current code

**There is no currency concept anywhere in the core money path.**

`swiftbon.account.service` and `swiftbon.postings.service` contain **zero** occurrences
of `currency`, `NGN`, `CAD`, or `USD` in any Go file, SQL file, migration, or proto.

Currency today is **implicit and single-valued**. The system is NGN by assumption in
four places:

1. **Ledger has one balance per account number, no currency dimension**
   `swiftbon.postings.service/migrations/000001_init_schema.up.sql`:
   ```sql
   CREATE TABLE posting_management.account_balances(
        "account_number" VARCHAR(20) NOT NULL,
        "ledger_balance" BIGINT NOT NULL DEFAULT(0),
        "checksum" TEXT NOT NULL,
        CONSTRAINT "uc_account_balance_account_number" UNIQUE ("account_number")
   );
   ```

2. **Nigerian CBN tier limits seeded as raw integers** (kobo), same migration:
   ```sql
   VALUES (..., 1,   5000000,  30000000,   5000000),   -- Tier 1: ₦50k / ₦300k
          (..., 2,  20000000,  50000000,  20000000),   -- Tier 2
          (..., 3, 500000000,        -1,        -1);   -- Tier 3, -1 = unlimited
   ```
   Enforced in `swiftbon.postings.service/internal/usecase/postTransaction.go:398-436`.

3. **Nigerian stamp duty hardcoded in config**
   `swiftbon.postings.service/config/config.yml:11-15` — `stamp_duty_threshold: 1000000`
   (₦10,000), `stamp_duty_amount: 5000` (₦50). Applied unconditionally in
   `internal/usecase/consumers/chargeStampDuty.go`.

4. **`+234` phone prefix hardcoded**
   `swiftbon.account.service/internal/util/string_manipulation.go:11` and
   `swiftbon.kyc.service/internal/util/string_manipulation.go:7`:
   ```go
   func AddCountryCodeToPhoneNumber(phonenumber string) string { return "+234" + phonenumber }
   ```

Currency must therefore be **added**, not changed. There is no wrong value to correct —
there is a missing dimension to introduce.

---

## 3. Canonical money model

### 3.1 Amounts

**Amounts stay `int64` minor units.** All five launch currencies have exponent 2, so the
existing `BIGINT` columns and `int64` fields remain correct. Do **not** convert amounts
to float or decimal.

Every amount must travel with its currency. Introduce this value type and use it at
every boundary (HTTP DTO, gRPC message, event payload, provider adapter):

```go
// internal/money/money.go
package money

// Money is an exact amount in the minor units of Currency.
// Amount 12345 with Currency "CAD" is $123.45.
type Money struct {
    Amount   int64  `json:"amount"`   // minor units, never fractional
    Currency string `json:"currency"` // ISO 4217 alpha-3, uppercase
}
```

### 3.2 Currency registry

Currency is **not** a free string and **not** an int enum. It is an ISO 4217 alpha-3
code validated against a registry that carries the exponent, so adding a
non-2-decimal currency later (JPY exponent 0, KWD exponent 3) does not silently corrupt
amounts.

```go
// internal/money/currency.go
package money

type Currency struct {
    Code     string // ISO 4217 alpha-3, uppercase
    Exponent int32  // decimal places in the minor unit
    Name     string
}

var registry = map[string]Currency{
    "CAD": {"CAD", 2, "Canadian Dollar"},
    "USD": {"USD", 2, "US Dollar"},
    "NGN": {"NGN", 2, "Nigerian Naira"},
    "GBP": {"GBP", 2, "Pound Sterling"},
    "EUR": {"EUR", 2, "Euro"},
}

func Lookup(code string) (Currency, error)  // uppercase + validate, error on unknown
func IsSupported(code string) bool
func Format(m Money) string                  // "CA$123.45", "₦1,234.56" — display only
```

**Rules:**

- Reject unknown or empty currency codes at the edge with a validation error. Never
  default a missing currency to anything.
- Normalise to uppercase on ingest.
- `Format` is for human-facing output only (notifications, statements). Never parse it back.

### 3.3 Rates are decimal, amounts are integer

FX rates are the **only** place decimals are allowed.

- Postgres: `NUMERIC(20,10)`
- Go: `github.com/shopspring/decimal`
- Never `float64`. There is an existing float round-trip bug to remove
  (`swiftbon.paylony.service/internal/usecase/worker/inboundTransaction.go:40-46`).

### 3.4 Conversion and rounding

```
destMinor = round_half_up( srcMinor × rate × 10^(destExponent − srcExponent) )
```

Rounding creates a residual that would otherwise unbalance the double entry. The
residual **must** be posted to an FX rounding GL account per currency. See §5.

---

## 4. Ledger invariants (non-negotiable)

These are the rules the postings service must enforce. Every other service depends on them.

1. **Double entry.** Every posting has ≥2 legs. `sum(debits) == sum(credits)`
   **per currency**, evaluated independently for each currency in the posting. A
   globally-balanced but per-currency-unbalanced posting is invalid.

2. **One currency per account.** An account number has exactly one currency, fixed at
   creation, immutable. A posting leg's currency must equal its account's currency.

3. **Atomicity.** All legs of a posting commit in a single database transaction, or none do.

4. **Row locking.** Every balance read that precedes a write uses `SELECT … FOR UPDATE`
   inside that transaction. Lock accounts in a deterministic order (sort by account
   number) to avoid deadlocks.

5. **Idempotency.** `(instrument_number, reference_number)` is unique. A replay returns
   the original result, not a second posting and not an error that hides the first.

6. **Ledger history is written in the same transaction as the balance change.** Never
   in a goroutine.

7. **Checksums include currency.** Balance rows carry a SHA-512 checksum over
   `account_number + ledger_balance + id`. Once `currency` is on the row it must be in
   the hash preimage, or a currency could be altered without detection.

8. **No negative balances** unless `force_debit` is set by an authorised system flow.

### 4.1 The atomicity defect you are fixing

There are two money-movement paths today and **they do not behave the same way**.

`POST /postings/v1/api/transfer/post` → `internal/usecase/postTransaction.go` — **correct**:

```go
tx, err := uc.db.Begin(cxt)          // postTransaction.go:49
defer tx.Rollback(cxt)
q := uc.repo.WithTx(tx)
// ... validates credits == debits at :40, all legs via q ...
if err := tx.Commit(cxt); err != nil { ... }   // :154
```

`POST /postings/v1/api/transfer/single` → `internal/usecase/processTransfer.go` — **broken**:

- 29 calls go directly to `uc.repo` with **no transaction**.
- It calls `GetLedgerBalance` (no lock), not `GetLedgerBalanceForUpdate` — the
  `FOR UPDATE` variant exists at
  `internal/entity/sql/query/account_balance.sql:8-10` and is unused here. In
  autocommit mode a `FOR UPDATE` lock is released immediately anyway.
- Debit and credit are separate autocommitted writes. If the credit fails after the
  debit succeeds, **the money is destroyed** and there is no compensating entry.
- Balance updates are read-modify-write with no version guard:
  `update … set ledger_balance = $1 … where id = $3`. Concurrent transfers lose updates.
- Ledger history is written in 8 fire-and-forget goroutines
  (`go uc.updateNominalAccountPostingHistory(...)`) using `context.Background()`.
  History can silently vanish.
- No `sum(credits) == sum(debits)` check at all.

**Resolution:** `postTransaction` becomes the single money-movement primitive.
`processTransfer` is reimplemented as a thin adapter that builds a balanced
`PostingRequest` and delegates to it. Do not add currency to `processTransfer`'s
existing logic — replace the logic.

---

## 5. FX design

### 5.1 Chart of accounts

For **each** supported currency, these system (GL) accounts must exist:

| Purpose | Why |
|---|---|
| `FX_POSITION_<CCY>` | Holds one side of a conversion. The pair of position accounts is where FX exposure accumulates. |
| `FX_ROUNDING_<CCY>` | Absorbs the sub-minor-unit rounding residual so the entry balances exactly. |
| `COLLECTION_<CCY>` | Inbound funds from providers. |
| `REVENUE_<CCY>` | Fees and spread earned. |
| `SUSPENSE_<CCY>` | Unreconciled items. |

`STAMP_DUTY_NGN` stays, but scoped to NGN only (see §6).

### 5.2 Quote then execute

FX is two steps. A rate is quoted, locked with a short TTL, then referenced by a posting.

**`POST /postings/v1/api/fx/quote`**

```json
{ "sell_currency": "CAD", "buy_currency": "USD", "sell_amount": 5000 }
```
```json
{
  "quote_id":     "b3f1...",
  "sell":         { "amount": 5000, "currency": "CAD" },
  "buy":          { "amount": 3650, "currency": "USD" },
  "rate":         "0.7300000000",
  "mid_rate":     "0.7336000000",
  "spread_bps":   49,
  "expires_at":   "2026-08-20T10:01:30Z"
}
```

**`POST /postings/v1/api/transfer/post`** with `"fx_quote_id": "b3f1..."`.

The posting engine expands one FX quote into four legs plus rounding:

```
debit   1001234567        CAD  50.00      customer CAD account
credit  FX_POSITION_CAD   CAD  50.00
debit   FX_POSITION_USD   USD  36.50
credit  1009876543        USD  36.50      customer USD account
```

Per-currency balance: CAD legs net to zero, USD legs net to zero. Any residual from
rounding posts to `FX_ROUNDING_<CCY>`.

### 5.3 Quote storage

```sql
CREATE TABLE posting_management.fx_quotes(
    id             uuid PRIMARY KEY DEFAULT UUID_GENERATE_V4(),
    created_at     timestamptz NOT NULL DEFAULT (now()),
    sell_currency  CHAR(3)        NOT NULL,
    buy_currency   CHAR(3)        NOT NULL,
    sell_amount    BIGINT         NOT NULL,
    buy_amount     BIGINT         NOT NULL,
    mid_rate       NUMERIC(20,10) NOT NULL,
    rate           NUMERIC(20,10) NOT NULL,   -- mid adjusted by spread
    spread_bps     INTEGER        NOT NULL,
    rate_source    VARCHAR(50)    NOT NULL,
    source_rate_id VARCHAR(255),
    expires_at     timestamptz    NOT NULL,
    consumed_at    timestamptz,
    consumed_by_reference VARCHAR(450),
    CONSTRAINT uc_fx_quotes_consumed_by_reference UNIQUE (consumed_by_reference)
);
```

**Rules:**

- A quote is single-use. Consuming it sets `consumed_at` **in the same transaction as
  the posting**. A second attempt fails.
- An expired quote is rejected. Never silently re-quote inside an execute call — the
  customer agreed to a specific rate.
- The applied `rate`, `quote_id`, and `rate_source` are persisted on the posting rows
  for audit. An MSB must be able to show the rate applied to any historical conversion.

### 5.4 Rates come from `swiftbon.fx.service`

See `15-fx-service-new.md`. Postings does not call rate providers directly.

---

## 6. Jurisdiction and de-Nigerianisation

Nigerian rules must become **scoped**, not deleted. NGN accounts still need them.

| Today | Target |
|---|---|
| Stamp duty applied to every transfer | A `levy_rules` table keyed by currency + posting type. Only NGN has a row. The engine applies whatever rules match; an empty match set means no levy. |
| Tier limits as bare integers | `account_tier_configuration` gains `currency CHAR(3)`; unique on `(tier_level, currency)`. Seed per currency. `-1` sentinel for unlimited stays but must be handled explicitly, never compared numerically. |
| `"+234" + phonenumber` | E.164 normalisation from the customer's country. Use `github.com/nyaruka/phonenumbers`. Never prefix-concatenate. |
| ID types: BVN, NIN, Voter's Card | Keep them for NG. Add for CA: passport, provincial driver's licence, PR card, SIN (store a hash or last-3 only, never plaintext). Make the valid set a function of country. |
| KYC via SmileID only | Route by jurisdiction. SmileID for NG; a Canadian provider for CA. See `06-kyc-service.md`. |

**Canadian regulatory context** (`swiftbon.account.service`, `swiftbon.kyc.service`,
`swiftbon.postings.service` all touch this): FINTRAC obligations for an MSB include
identity verification at onboarding, PEP and sanctions screening, 24-hour rule
aggregation, Large Cash Transaction Reports, Electronic Funds Transfer Reports for
international transfers at or above CAD 10,000, Suspicious Transaction Reports, and
five-year record retention.

> Treat these as **engineering requirements to build the hooks for**, not as a compliance
> specification. Reporting thresholds and filing mechanics must be confirmed with
> Swiftbon's compliance officer before go-live. Where a prompt says "emit a reportable
> event", build the event and the store; do not hardcode a filing decision.

---

## 7. Architectural facts you need

Established by direct inspection. Do not re-derive these.

**Stack (all 14 repos).** Go, Echo v4 HTTP, gRPC, `pgx/v5`, **sqlc** (queries are
hand-written SQL in `internal/entity/sql/query/*.sql`, generated code committed to
`internal/usecase/repo/<name>Db/`), `golang-migrate`, `cleanenv` (YAML then ENV
override), `logrus` + `lumberjack`, swaggo.

**Layout (identical everywhere).**
```
cmd/{app,consumer,worker,cronjob,webhook,webhookWorker}/main.go
config/{config.go,config.yml}
internal/app/app.go                    # constructor DI, no framework
internal/controller/http/v1/            # Echo router + handlers
internal/controller/grpc/
internal/controller/pubsub/seahorse/    # event consumer
internal/entity/sql/{query,schema}/     # sqlc inputs
internal/enums/  internal/models/  internal/middleware/  internal/util/
internal/usecase/{,repo/,webapi/,consumers/,worker/}
migrations/  pkg/{httpserver,grpcserver,postgres,logger}/
```

**`seahorse` is the event bus.** Not a gateway, not a library. It is a homegrown
Postgres-backed pub/sub broker (module `persianblack.com/seahorse`) with gRPC **push**
delivery: services `RegisterConsumer` at boot with their own address, seahorse calls
`ConsumeEvent` on them, they ack with `MarkEventAsProcessed`. At-least-once.

**Kafka and RabbitMQ are dead code** in every repo. `pkg/kafka` and `pkg/rabbitmq`
exist and are unwired. Do not build on them.

**There is no shared Go module.** Zero `replace` directives, zero cross-repo imports.
`pkg/`, `internal/models`, `internal/enums`, `internal/middleware`, `internal/util`, and
even `.proto` files are **copy-pasted** per repo and have drifted.

> This matters enormously for this project. A `Money` type that is copy-pasted 14 times
> will diverge. **Prompt `01` creates `swiftbon.shared` as a real Go module** and every
> subsequent prompt consumes it. Do that first.

**Service topology.** Database-per-service on managed Postgres. Deployment is *not*
Kubernetes despite the `kubernetes/` directories — it is GitHub Actions → `rsync`
binary over SSH → `systemd`, fronted by Caddy. The `kubernetes/` dirs are dead legacy.

**Who calls whom in the money path.**
```
billers / flights / juice / paystack / paylony
        │  gRPC PostTransaction / ReverseTransaction
        ▼
swiftbon.postings.service ──gRPC SearchCustomerByAccountNumber──▶ swiftbon.account.service
        │
        │  publishes "transaction-completed" via seahorse
        ▼
swiftbon.communication.server ──▶ chatbot (WhatsApp alerts)
```
Account creation is event-driven: account service publishes `account-created`, postings
consumes it and runs `InitialiseLedgerBalance`.

---

## 8. Known defects to fix as you pass through

These are real, verified, and all sit in the money path. Each prompt names the ones in
its repo. Listed here so you recognise them.

| # | Defect | Location |
|---|---|---|
| 1 | Non-transactional debit+credit; money can be destroyed | `postings/internal/usecase/processTransfer.go` |
| 2 | Ledger history written in fire-and-forget goroutines | same, 8 sites |
| 3 | Read-modify-write balances with no lock or version guard | same |
| 4 | Truncating integer division drops sub-unit amounts: `transaction.Amount/100` | `paylony/internal/usecase/worker/outboundTransaction.go:18` |
| 5 | Float round-trip on collections; `ParseFloat(s, 10)` has an invalid bitSize and `int64(f*100)` truncates | `paylony/internal/usecase/worker/inboundTransaction.go:40-46` |
| 6 | Webhook signature verification commented out | `juice/internal/usecase/webhook.go:20-26` |
| 7 | Webhook signature verification entirely absent | `paystack/internal/usecase/webhook.go`, `paylony/internal/usecase/webhook.go` |
| 8 | Reserved funds stranded — payout failure has no reversal path (`//TODO: fire event to reverse funds`) | `paystack` and `paylony` workers |
| 9 | Fee/revenue subtracted without a floor check, so a small transfer yields a negative credit leg | `juice/internal/usecase/webapi/reserveFunds.go:62` |
| 10 | `md.Get("x-client-id")[0]` panics on metadata without that key | every gRPC handler, all repos |
| 11 | All internal gRPC is plaintext `insecure.NewCredentials()` with a shared static client-id — including the money path | every repo |
| 12 | Live-looking credentials committed to `config/config.yml` and `.env.example` | every repo |
| 13 | `bani`'s `InitateOutbound` is a copy-pasted Twilio SMS function | `bani/internal/usecase/webapi/initiateOutbound.go` |
| 14 | CI pins Go 1.17 against `go 1.24` modules, so the test job cannot pass | every repo |
| 15 | `flights.service` is a byte-identical copy of `billers.service`, module still `swiftbon.com/billers` | `swiftbon.flights.service` |

> **#12 is urgent and independent of this project.** Rotate every committed credential
> and move it to the deployment secret store. Do not let a multi-currency PR be the
> thing that finally touches those files. Raise it separately.

---

## 9. Conventions to follow

- **Never** widen a currency-carrying field to nullable-with-default. A missing currency
  is a validation error, not a defaultable value.
- Response envelope stays `models.BaseResponse` with the existing string codes
  (`00` success, `02` validation, `06` not found, `98` SQL, `99` general). Business
  failures return HTTP 200 with a non-`00` code — that is the house style; keep it.
  Add new codes rather than reusing `99`.
- Migrations are `migrations/NNNNNN_<name>.{up,down}.sql`, sequential, with a real
  `down`. Every migration in this project must be backward compatible for one deploy:
  add nullable → backfill → enforce `NOT NULL`. Three migrations, not one.
- After editing `internal/entity/sql/query/*.sql`, run `sqlc generate` and commit the
  output.
- After changing a `.proto`, regenerate (`make generate-protoc`) **and** update every
  copied duplicate. Prompt `01` removes the duplication; until then, grep for the file
  name across all repos.
- There are **no unit tests** anywhere today, only one network-level integration test
  per repo (several stale). Every prompt in this set requires tests. Money code without
  tests does not ship.

---

## 10. Order of execution

Dependencies are real. This order is not a suggestion.

```
01  swiftbon.shared           ← shared module: money, currency, contracts     FOUNDATION
15  swiftbon.fx.service       ← rate source, needed before FX can execute
02  swiftbon.account.service  ← currency on accounts, Canadian KYC fields
03  swiftbon.postings.service ← currency on ledger, atomicity, FX postings    CORE
04  seahorse                  ← versioned event contracts
─────────────────────────────── core money path complete, verify end-to-end ───
05  swiftbon.juice.service        ─┐
06  swiftbon.kyc.service           │
07  swiftbon.paystack.service      ├─ can run in parallel
08  swiftbon.paylony.service       │
09  swiftbon.bani.service         ─┘
─────────────────────────────────────────────────────────────────────────────
10  swiftbon.billers.service
11  swiftbon.flights.service   ← re-fork from billers, do not patch in place
12  swiftbon.communication.server
13  swiftbon.chatbot.whatsapp.service
14  swiftbon.com.backend.go.template   ← refresh scaffold last, from the result
16  swiftbon.go.deployment.template    ← config/secret plumbing
```

**Checkpoint after `04`.** Do not start the provider services until you can, on a local
stack: open a CAD and a USD account for one customer, fund the CAD account, quote
CAD→USD, execute the conversion, and see both balances and four balanced ledger legs
with the rate recorded. `03-postings-service.md` §Verification has the exact commands.

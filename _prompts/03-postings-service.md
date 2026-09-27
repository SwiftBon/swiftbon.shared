# 03 — `swiftbon.postings.service` — multi-currency ledger, FX postings, atomicity

> Read `00-READ-FIRST-shared-context.md` first.
> Requires `01-shared-module-new.md`, `02-account-service.md`, `15-fx-service-new.md`.

**This is the most consequential prompt in the set.** This service is the ledger. If it
is wrong, money is wrong. Work carefully and do not skip the tests.

---

## Prompt

You are working in
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.postings.service`
(module `swiftbon.com/postings`, Go 1.24.1, Echo v4 + gRPC + sqlc + pgx/v5).

Swiftbon is a Canadian licensed MSB. This service is the core banking ledger. It must
hold balances in CAD, USD, NGN, GBP, and EUR, post same-currency and cross-currency
(FX) transactions, and do so atomically.

### Current state (verified — do not re-derive)

**The ledger has no currency dimension.** From `migrations/000001_init_schema.up.sql`:

```sql
CREATE TABLE posting_management.account_balances(
     "account_number" VARCHAR(20) NOT NULL,
     "ledger_balance" BIGINT NOT NULL DEFAULT(0),
     "checksum" TEXT NOT NULL,
     CONSTRAINT "uc_account_balance_account_number" UNIQUE ("account_number")
);
```

`system_account_balances` is identical. `posting_history`,
`nominal_account_transaction_history`, and `system_account_tranaction_history` [sic —
the table name is misspelled in the schema] all carry `amount BIGINT` with no currency.
The repo contains **zero** occurrences of `currency` in any Go or SQL file.

**Amounts are `int64` minor units** (kobo, implied, never documented). Note the schema
writes `BIGINT NOT NULL DEFAULT(0.00)` in places — a decimal-looking default on an
integer column, which is misleading but harmless.

**There are two money paths and they do not behave the same way.**

`POST /transfer/post` → `internal/usecase/postTransaction.go` — **correct**:
```go
if credits != debits { /* reject */ }        // :40
tx, err := uc.db.Begin(cxt)                  // :49
defer tx.Rollback(cxt)
q := uc.repo.WithTx(tx)                      // :57
// ... all legs through q ...
if err := tx.Commit(cxt); err != nil { … }   // :154
```

`POST /transfer/single` → `internal/usecase/processTransfer.go` — **broken**:
- 29 direct `uc.repo` calls with **no transaction**.
- Calls `GetLedgerBalance` (unlocked). `GetLedgerBalanceForUpdate` exists at
  `internal/entity/sql/query/account_balance.sql:8-10` and is not used here. In
  autocommit a `FOR UPDATE` lock is released immediately regardless.
- Debit then credit as separate autocommitted writes:
  ```go
  response, openingBalance, closingBalance, err := uc.debitNominalAccountTx(...)   // :88
  go uc.updateNominalAccountPostingHistory(context.Background(), ...)              // :96
  openingBalance, closingBalance, err = uc.creditNominalAccountTx(...)             // :98
  if err != nil { return nil, err }   // sender already debited — money destroyed
  ```
- 8 fire-and-forget `go uc.…` history writes. Ledger history can silently vanish.
- Read-modify-write balance update with no version guard:
  `update … set ledger_balance = $1, checksum = $2 where id = $3`. Concurrent transfers
  lose updates.
- No `credits == debits` check.

**Balance integrity uses a SHA-512 checksum** over
`fmt.Sprintf("%s%d%x", AccountNumber, LedgerBalance, ID.Bytes)`, verified on read and
regenerated on write, at roughly 10 sites (`processTransfer.go:68`, `:83`, `:345`, …;
helpers in `internal/util/checksum.go`).

**Nigerian rules are hardcoded.** Tier limits seeded as raw kobo integers (CBN 3-tier
KYC), enforced at `postTransaction.go:398-436` with `if accountTier != 3` bypassing
limits entirely for tier 3. Stamp duty in `config/config.yml:11-15`
(`stamp_duty_threshold: 1000000`, `stamp_duty_amount: 5000`), applied unconditionally by
`internal/usecase/consumers/chargeStampDuty.go`. `ReversalResponse` has a
`StampDutyReversalReference` field. A log line in `debitNominalAccountTx` says "kobo".

**`account_tier_configuration`** holds `maximum_daily_withdrawal_limit`,
`cumulative_balance_limit`, `single_deposit_limit` as bare `BIGINT`, with `-1` meaning
unlimited.

**Events.** Consumes `account-created` → `InitialiseLedgerBalance`,
`system-account-created`, `reverse-transaction`, `charge-stamp-duty`. Publishes
`transaction-completed` (`internal/models/eventModels.go`) whose payload has
`OpeningBalance`, `ClosingBalance`, `LedgerBalancer` [sic], `Amount`, `AllowedAmount`,
`LienAmount` — all `int64`, **no currency**, so downstream alerting cannot tell what
currency it is formatting.

---

## Tasks

Do them in this order. Tasks 1–3 must land before 4–6; posting FX onto a
non-atomic ledger would bake the defect in permanently.

### 1. Adopt `swiftbon.shared`

Add the `replace` directive. Replace every `int64` amount at a boundary (HTTP DTO, gRPC
message, event payload) with `money.Money`. Internal arithmetic can stay `int64` inside
a single-currency scope, but every value crossing a function boundary carries its
currency.

Resolve the `AccountType` `Current`/`Checking` drift with `account.service` — same
integer, different name today.

### 2. Currency on the ledger

Because the model is **account per currency** (see `00 §1`), each account number has
exactly one balance — so the existing `UNIQUE (account_number)` constraint stays valid.
Currency is added as a column that must **match the owning account**, not as part of the
balance key.

```sql
-- 000003_add_ledger_currency.up.sql   (nullable first)
ALTER TABLE posting_management.account_balances         ADD COLUMN currency CHAR(3);
ALTER TABLE posting_management.system_account_balances  ADD COLUMN currency CHAR(3);
ALTER TABLE posting_management.posting_history          ADD COLUMN currency CHAR(3);
ALTER TABLE posting_management.nominal_account_transaction_history      ADD COLUMN currency CHAR(3);
ALTER TABLE posting_management.system_account_tranaction_history        ADD COLUMN currency CHAR(3);
ALTER TABLE posting_management.account_lien             ADD COLUMN currency CHAR(3);
ALTER TABLE posting_management.nominal_account_daily_transactions       ADD COLUMN currency CHAR(3);
ALTER TABLE posting_management.account_debts            ADD COLUMN currency CHAR(3);

-- 000004_backfill_ledger_currency.up.sql
-- Every pre-existing row is Nigerian naira. This is a statement of fact about
-- historical data, not a default for new rows.
UPDATE posting_management.account_balances        SET currency='NGN' WHERE currency IS NULL;
-- … same for each table …

-- 000005_enforce_ledger_currency.up.sql
ALTER TABLE posting_management.account_balances ALTER COLUMN currency SET NOT NULL;
ALTER TABLE posting_management.account_balances
  ADD CONSTRAINT ck_account_balances_currency
  CHECK (currency IN ('CAD','USD','NGN','GBP','EUR'));
-- … same for each table …
```

**The checksum must include currency.** Change the preimage everywhere it appears:

```go
// before
fmt.Sprintf("%s%d%x", b.AccountNumber, b.LedgerBalance, b.ID.Bytes)
// after
fmt.Sprintf("%s%s%d%x", b.AccountNumber, b.Currency, b.LedgerBalance, b.ID.Bytes)
```

Without this, a currency could be altered in the database without invalidating the
checksum — which defeats the point of having one.

This changes the hash of **every existing row**. The backfill migration must recompute
and rewrite all checksums in the same transaction as the currency backfill. Write it as
a single migration that does both, verify the row count, and make the `down` migration
restore the old preimage. Test the round trip on a seeded database before running it
anywhere real.

Centralise the preimage in one function — `util.BalanceChecksumPreimage(...)` — so the
next change to it is a one-line edit rather than ten.

### 3. One atomic money-movement primitive

**`postTransaction` becomes the only path that moves money.** Reimplement
`ProcessTransfer` as a thin adapter: build a balanced two-leg `PostingRequest` from the
`ProcessTransferRequest` and delegate. Do not add currency to `processTransfer.go`'s
existing logic — delete the logic.

The primitive must guarantee, in a single `pgx` transaction:

1. **Per-currency balance.** `sum(debits) == sum(credits)` **for each currency
   independently**. This is the critical change from today's single global check — an FX
   posting is globally unbalanced by construction and only balances per currency.
2. **Currency agreement.** Each leg's currency equals its account's currency. Mismatch →
   reject.
3. **Deterministic locking.** Collect every account in the posting, sort by account
   number, then `SELECT … FOR UPDATE` in that order. Prevents deadlock between two
   concurrent transfers touching the same pair in opposite directions.
4. **Locked reads.** Replace `GetLedgerBalance` with `GetLedgerBalanceForUpdate` on every
   read that precedes a write.
5. **History in-transaction.** Every `posting_history` and
   `*_transaction_history` insert happens on `q := uc.repo.WithTx(tx)`. **Delete all 8
   fire-and-forget goroutines.** If a history write fails, the whole posting rolls back.
6. **Idempotency.** `(instrument_number, reference_number)` unique. A replay returns the
   original posting's result with `00` — not a duplicate, and not an error that hides
   the first posting. Add the unique index; the current check is a race
   (`CheckForDuplicateTransaction` then insert, non-atomically).
7. **Checksum verified under lock**, and regenerated before commit.

Add an optimistic guard as defence in depth even with row locks:
```sql
-- name: UpdateLedgerBalance :execrows
update posting_management.account_balances
   set ledger_balance = $1, checksum = $2, last_updated_at = now()
 where id = $3 and ledger_balance = $4;   -- expected prior balance
```
Zero rows affected → abort the transaction.

> Preserve the existing gRPC and HTTP contracts. `billers`, `flights`, `juice`,
> `paystack`, and `paylony` all call `PostTransaction` / `ProcessSingleTransfer`, and
> they are updated in later prompts. Keep `/transfer/single` responding with the same
> shape while its internals change.

### 4. Per-currency and per-jurisdiction rules

**Tier limits become per-currency.**

```sql
ALTER TABLE posting_management.account_tier_configuration ADD COLUMN currency CHAR(3);
UPDATE posting_management.account_tier_configuration SET currency='NGN' WHERE currency IS NULL;
ALTER TABLE posting_management.account_tier_configuration ALTER COLUMN currency SET NOT NULL;
ALTER TABLE posting_management.account_tier_configuration
  DROP CONSTRAINT IF EXISTS …,
  ADD CONSTRAINT uc_tier_config_level_currency UNIQUE (tier_level, currency);
```

Seed CAD, USD, GBP, EUR tiers. **Do not convert the Nigerian limits into other
currencies** — they encode CBN policy, not an amount. Canadian limits are a compliance
decision: seed conservative placeholders, mark them clearly as
`-- PLACEHOLDER: confirm with compliance before go-live`, and surface that in the PR.

Fix the `-1` sentinel handling: it currently participates in numeric comparison. Treat it
as an explicit "unlimited" branch, never as a number. Also remove the
`if accountTier != 3` blanket bypass at `postTransaction.go:424` — tier 3 should have
limits read from configuration, even if those limits are unlimited.

**Stamp duty becomes a scoped levy rule.** Replace the hardcoded config with a table:

```sql
CREATE TABLE posting_management.levy_rules(
    id uuid PRIMARY KEY DEFAULT UUID_GENERATE_V4(),
    created_at timestamptz NOT NULL DEFAULT (now()),
    levy_name VARCHAR(50) NOT NULL,
    currency CHAR(3) NOT NULL,
    posting_type SMALLINT,           -- NULL = any
    transaction_method SMALLINT,     -- NULL = any
    threshold_amount BIGINT NOT NULL,
    levy_amount BIGINT NOT NULL,
    levy_account_number VARCHAR(20) NOT NULL,
    entry_code VARCHAR(20) NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT(TRUE)
);
```

Seed exactly one row: NGN stamp duty, threshold `1000000`, amount `5000`, from the
current config values. A CAD transfer matches no rule and is levied nothing. An empty
match set means no levy — never a fallback.

Keep `StampDutyReversalReference` on `ReversalResponse` for wire compatibility but stop
populating it for non-NGN; add a general `levy_reversal_references []string`.

### 5. FX postings

Consume quotes from `swiftbon.fx.service` (prompt 15). Postings does **not** call rate
providers.

Add `fx_quote_id` (optional) to `PostingRequest`. When present:

1. Fetch the quote from `fx.service` by ID. Reject if expired, already consumed, or if
   its currencies do not match the posting's legs.
2. Expand into a balanced multi-currency entry:
   ```
   debit   <customer CAD acct>    CAD  50.00
   credit  FX_POSITION_CAD        CAD  50.00
   debit   FX_POSITION_USD        USD  36.50
   credit  <customer USD acct>    USD  36.50
   ```
   CAD legs net to zero; USD legs net to zero. Per-currency balance holds.
3. Post the rounding residual from `money.Convert` to `FX_ROUNDING_<CCY>` so the entry
   balances exactly. **Never** discard the residual and never adjust the customer leg to
   absorb it — the customer gets the amount they were quoted.
4. Persist `fx_quote_id`, `rate`, and `rate_source` on the `posting_history` rows. An
   MSB must be able to produce the rate applied to any historical conversion.
5. Mark the quote consumed **in the same transaction** as the posting. Single-use.

Store quotes locally (schema in `00 §5.3`) so the audit trail does not depend on
`fx.service` remaining available.

Add `POST /postings/v1/api/fx/convert` as a convenience that quotes and executes in one
call for internal callers — but it must go through the same primitive and the same quote
lifecycle, not a shortcut.

**Chart of accounts.** Create the per-currency system accounts from `00 §5.1`
(`FX_POSITION_*`, `FX_ROUNDING_*`, `COLLECTION_*`, `REVENUE_*`, `SUSPENSE_*`) as seed
data, and add a startup check that fails loudly if a supported currency is missing its
GL accounts. Discovering a missing `FX_ROUNDING_EUR` at posting time is much worse than
at boot.

### 6. Currency-aware reads and events

- `AccountBalanceRequest` / `AccountBalanceResponse`: return `money.Money` and include
  currency. Add an endpoint returning **all** balances for a customer across currencies —
  the app needs this and cannot currently get it.
- `GetTransactionHistoryResponse`: include currency per row.
- `transaction-completed` event: add `currency` and `schema_version: 2`. This is what
  `communication.server` uses to format alerts; without it, it prints `₦` on a CAD
  transaction. Coordinate with `04-seahorse.md` and `12-communication-server.md`.
- `InitialiseLedgerBalance` (the `account-created` consumer): read `currency` from the
  event and set it on the balance row. Tolerate `schema_version: 1` events by treating
  them as NGN during rollout, and log a warning when you do.

### 7. Canadian regulatory hooks

Build the mechanism; leave the filing policy to compliance.

- **24-hour aggregation.** `nominal_account_daily_transactions` already accumulates per
  account per day but has no currency and no rolling window. Add currency, and add a
  query that aggregates transactions per customer per currency over a rolling 24 hours.
- **Reportable-threshold events.** When an aggregate crosses a configured threshold
  (e.g. CAD 10,000 for an international EFT), emit a `reportable-transaction` event and
  persist a row. Do **not** hardcode a filing decision or block the transaction.
- **Retention.** Confirm nothing prunes `posting_history`. Five-year retention is
  required; there is no reaper today, which is the correct state — keep it that way and
  document it.

> Thresholds and reportability must be confirmed with Swiftbon's compliance officer
> before go-live. Seed them as configuration with a clear placeholder marker.

### 8. Defects to fix in passing

- `md.Get("x-client-id")[0]` panics on metadata without that key.
- No overflow guards on balance arithmetic
  (`ledgerBalance + totalPositiveDebts - totalNegativeDebts`). Use the shared
  overflow-checked helpers.
- `account_debts.interest_percentage` is `BIGINT` with undocumented scale. Document it
  or move it to `NUMERIC`. Do not guess the scale — find a caller first.
- `internal/usecase/interfaces.go` exposes raw ledger primitives
  (`DebitNominalAccount`, `CreditNominalAccount`, `HandleSystemAccountDebit`, …). Once
  task 3 lands these are unsafe to call individually. Make them unexported or remove
  them from the interface.
- CI pins Go 1.17 against a `go 1.24.1` module, so the test job cannot pass.

---

## Verification

This is the checkpoint gate from `00 §10`. Everything below must pass before starting
the provider-service prompts.

```bash
cd /Users/princeokechukwu/source/repos/swiftbon/swiftbon.postings.service
make compose-up && make migrate-up
make test    # -race, and this must now be non-vacuous
```

**End-to-end multi-currency, with account and fx services running:**

```bash
# 1. open CAD + USD accounts (account.service) -> two account-created events
# 2. confirm two balance rows exist with the right currencies
curl -s localhost:8103/postings/v1/api/account/balance \
  -H 'x-client-id: …' -d '{"account_number":"<cad_acct>"}' | jq
# expect {"amount":0,"currency":"CAD"}

# 3. fund the CAD account (GL -> account)
# 4. quote CAD->USD
curl -s localhost:<fx>/fx/v1/api/quote -H 'x-client-id: …' \
  -d '{"sell_currency":"CAD","buy_currency":"USD","sell_amount":5000}' | jq
# 5. execute with the quote_id
curl -s localhost:8103/postings/v1/api/transfer/post -H 'x-client-id: …' \
  -d '{"fx_quote_id":"…", …}' | jq
```

Then assert in the database:

```sql
-- four legs, two currencies, balanced per currency
SELECT currency, posting_record_type, sum(amount)
  FROM posting_management.posting_history
 WHERE reference_number = '<ref>'
 GROUP BY currency, posting_record_type;
-- for each currency, debit total must equal credit total

-- the rate is on the audit trail
SELECT DISTINCT currency, fx_quote_id, rate FROM posting_management.posting_history
 WHERE reference_number = '<ref>';

-- the quote is single-use
SELECT consumed_at, consumed_by_reference FROM posting_management.fx_quotes
 WHERE id = '<quote_id>';
```

**Failure and concurrency tests — these are the ones that matter:**

- Replay the same `(instrument_number, reference_number)`. Expect the original result,
  exactly one posting, and no balance change.
- Force the credit leg to fail mid-posting (inject an error after the debit). Expect a
  full rollback: sender balance unchanged, zero history rows. **On the current code this
  test destroys money — it is the regression test for the whole prompt.**
- Reuse a consumed `fx_quote_id`. Expect rejection.
- Use an expired quote. Expect rejection.
- Post a cross-currency entry with mismatched leg currencies. Expect rejection.
- Post a leg whose currency differs from its account's currency. Expect rejection.
- Fire 50 concurrent transfers against one account and assert
  `final == initial - sum(debits)` exactly, with `posting_history` row count matching.
  This is the lost-update test.

## Acceptance criteria

- [ ] Balances exist and are correct in all five currencies.
- [ ] `sum(debits) == sum(credits)` is enforced **per currency**.
- [ ] A leg whose currency differs from its account's currency is rejected.
- [ ] All money movement goes through one transactional primitive; `processTransfer`'s
      non-transactional path is gone and no `go uc.…` history writes remain.
- [ ] Balance reads that precede writes use `FOR UPDATE`, in deterministic order.
- [ ] Checksum preimage includes currency; all existing rows recomputed; `down` restores.
- [ ] Duplicate `(instrument, reference)` is idempotent, enforced by a unique index.
- [ ] The concurrency test passes with no lost updates.
- [ ] The injected-failure test shows a full rollback with no money destroyed.
- [ ] FX posts four balanced legs with the residual on `FX_ROUNDING_*`, quote consumed
      atomically, rate persisted.
- [ ] Stamp duty applies to NGN only; a CAD transfer is levied nothing.
- [ ] Tier limits are per-currency; CAD placeholders flagged for compliance.
- [ ] `transaction-completed` carries currency.
- [ ] Startup fails loudly if a supported currency lacks its GL accounts.

## Out of scope

- Rate sourcing and spread policy — `15-fx-service-new.md`.
- Provider payouts and collections — prompts 05–09.
- Alert formatting — `12-communication-server.md`.
- Replacing the shared-static-client-id auth on the money path. It is a real weakness
  (plaintext gRPC, one shared secret) but it is a separate security workstream. Note it
  in the PR; do not fold it in.

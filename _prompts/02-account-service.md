# 02 — `swiftbon.account.service` — multi-currency accounts + Canadian onboarding

> Read `00-READ-FIRST-shared-context.md` first. Requires `01-shared-module-new.md`.

---

## Prompt

You are working in
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.account.service`
(module `swiftbon.com/accountms`, Go 1.24.1, Echo v4 + gRPC + sqlc + pgx/v5).

Swiftbon is a Canadian licensed MSB. This service owns customers and their accounts. It
must let a customer hold **one account per currency** across CAD, USD, NGN, GBP, and EUR,
and it must onboard Canadian customers — today it can only onboard Nigerian ones.

### Current state (verified — do not re-derive)

**`customer_accounts` has no currency column.** From
`migrations/000001_init_schema.up.sql`:

```sql
CREATE TABLE account_management.customer_accounts(
     "account_number" VARCHAR(12) NOT NULL,
     "owner_id" uuid NOT NULL,
     "owner_type" INTEGER NOT NULL DEFAULT(1),
     "product_id" uuid NOT NULL,
     "branch_id" uuid NOT NULL,
     "account_type" INTEGER NOT NULL DEFAULT(1),
     "account_status" INTEGER NOT NULL DEFAULT(1),
     "transaction_permission" INTEGER NOT NULL DEFAULT(1),
     "tier_level" INTEGER NOT NULL DEFAULT(0),
     -- no currency
     CONSTRAINT "uc_customer_accounts_account_number" UNIQUE ("account_number")
);
```

The whole repo contains **zero** occurrences of `currency`, `CAD`, `USD`, or `NGN`.

**The `customers` table is Nigeria-shaped.** `bvn`, `lga`, `state`, `hometown`,
`religion`, `marrital_status` [sic], plus `scuml_business_type` and `tin_number` on
`groups`. Seed data is `Head Office / Lekki, Lagos`.

**`CreateCustomer` collects less than the schema holds.**
`internal/models/requestModels.go:9-29` has no address, no country, no nationality, and
no currency — despite `customers` having `address`, `city`, `state`, `country`,
`nationality` columns. For a FINTRAC-regulated MSB, address and ID are mandatory at
onboarding, so this is a gap independent of currency.

**Account numbers are generated in Postgres.** `generate_customer_account_number(2, 10)`
— 10 digits with a leading `2` — called from
`internal/entity/sql/query/customer_account.sql:10`. Column is `VARCHAR(12)` here but
`VARCHAR(20)` in postings.

**ID types are Nigeria-only.** `internal/enums/id_type.go`: `BVN`, `NIN_V2`, `NIN_Slip`,
`V_NIN`, `Drivers_License`, `VotersCard`, `BankAccount`, `Phonenumber`.

**Phone numbers are hardcoded Nigerian.**
`internal/util/string_manipulation.go:11,15,27`:
```go
func AddCountryCodeToPhoneNumber(phonenumber string) string { return "+234" + phonenumber }
```

**`AccountType` has already drifted from postings.** `internal/enums/account_type.go`
declares `Checking` at index 2; `postings/internal/enums/account_type.go` declares
`Current` at index 2. Same integer, different name, two services. Also note
`String()` indexes a 4-element array with values up to 7 — `AccountType(5).String()`
panics.

**Routes** — `internal/controller/http/v1/router.go:79-88`, all under `account/v1/api`
behind a static `x-client-id` check:
```
POST /account/create                              createCustomer
POST /account/update                              updateCustomer
POST /customer/account/update                     updateCustomerAccount
POST /account/search/:searchparam                 searchCustomer
POST /account/searchbyaccountnumber/:accountnumber searchCustomerByAccount
POST /account/check/:phonenumber                  checkIfCustomerExists
POST /system/account/create                       createSystemAccount
POST /system/account/searchbyaccountnumber/:accountnumber searchSystemAccountByAccount
```

**Events.** Publishes `account-created` and `system-account-created` via seahorse;
consumes `bvn-verified`, `means-of-identification-verified`,
`address-verification-successful`. `postings` consumes `account-created` and runs
`InitialiseLedgerBalance` — **so the ledger's currency comes from this event.** The
event model in `internal/models/eventModels.go` carries no currency; that is the single
most important contract to fix here.

---

## Tasks

### 1. Adopt `swiftbon.shared`

Add the `replace` directive from prompt 01. Use `money.Money` and `money.Lookup` for
every amount and currency. Delete the local `AccountType` enum in favour of the shared
one, resolving the `Checking`/`Current` drift explicitly (pick one, migrate the other
service's data if any rows use it) and fix the panicking `String()` while you are there.

### 2. Currency on accounts

Three migrations, backward compatible for one deploy — add nullable, backfill, enforce:

```sql
-- 000004_add_account_currency.up.sql
ALTER TABLE account_management.customer_accounts ADD COLUMN currency CHAR(3);
ALTER TABLE account_management.system_accounts   ADD COLUMN currency CHAR(3);

-- 000005_backfill_account_currency.up.sql
UPDATE account_management.customer_accounts SET currency = 'NGN' WHERE currency IS NULL;
UPDATE account_management.system_accounts   SET currency = 'NGN' WHERE currency IS NULL;

-- 000006_enforce_account_currency.up.sql
ALTER TABLE account_management.customer_accounts ALTER COLUMN currency SET NOT NULL;
ALTER TABLE account_management.system_accounts   ALTER COLUMN currency SET NOT NULL;
ALTER TABLE account_management.customer_accounts
  ADD CONSTRAINT ck_customer_accounts_currency
  CHECK (currency IN ('CAD','USD','NGN','GBP','EUR'));
ALTER TABLE account_management.customer_accounts
  ADD CONSTRAINT uc_customer_accounts_owner_product_currency
  UNIQUE (owner_id, owner_type, product_id, currency);
```

The backfill to `NGN` is correct: every existing account is a Nigerian naira account.
State that in the migration as a comment so it is not mistaken for a default.

The new unique constraint is what enforces the model: **one account per
(owner, product, currency)**. A customer asking twice for a CAD retail account gets the
existing one.

> Do **not** encode currency in the account number. It is tempting (a `2` prefix for
> NGN, `3` for CAD) and it will hurt: it constrains the number space, leaks product
> structure, and breaks the moment a currency is added. Keep numbers opaque; the
> `currency` column is the source of truth.

### 3. Account-opening API

Currency becomes an explicit, required input. Split account opening from customer
creation — they are different operations and conflating them is why there is no way to
open a second account today.

**`POST /account/v1/api/customer/account/open`** (new)

```json
{ "customer_id": "…", "currency": "CAD", "account_type": 2, "product_id": "…" }
```

- `currency` required; validate with `money.Lookup`. Unknown → `02` validation error.
- Idempotent: if an account already exists for that (owner, product, currency), return it
  with `00` rather than erroring or creating a duplicate.
- Reject if the customer's KYC state does not permit that currency (see task 5).

**`GET /account/v1/api/customer/:id/accounts`** (new) — list all accounts with currency,
so callers can find the right account to debit.

**`GET /account/v1/api/currencies`** (new) — `money.Supported()`, so the app and chatbot
do not hardcode the list.

`POST /account/create` keeps working. Give it an **optional** `initial_currency`; when
present, open that account, when absent, open the product's default. Do not silently
assume NGN — make the default explicit in product configuration (task 4).

`CreateSystemAccount` must take a currency; its `InitialBalance int64` becomes
`money.Money`.

### 4. Products carry currency policy

`products` currently has only `product_name`, `product_description`, `is_active`. Add
which currencies a product may be held in and which is the default:

```sql
CREATE TABLE account_management.product_currencies(
    id uuid PRIMARY KEY DEFAULT UUID_GENERATE_V4(),
    product_id uuid NOT NULL REFERENCES account_management.products(id),
    currency CHAR(3) NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT(FALSE),
    CONSTRAINT uc_product_currencies UNIQUE (product_id, currency)
);
```

Seed the existing `retail` product with all five, default CAD (Swiftbon is a Canadian
MSB; CAD is the home currency). Add a partial unique index so exactly one default per
product is possible.

### 5. Canadian onboarding

Keep Nigerian support intact — NGN accounts still need BVN and NIN. Make the required
field set a **function of the customer's country**, not a global.

**Customer fields.** `CreateCustomer` must collect and persist what it currently drops:
`address`, `city`, `country` (ISO 3166-1 alpha-2), `nationality`, plus a new
`postal_code` and a `subdivision` column that means "state" for NG and "province" for CA.
Rename nothing destructively — add `subdivision` and backfill from `state`.

Make `religion`, `marrital_status`, `hometown`, `lga` nullable and stop requiring them.
They are not meaningful for Canadian onboarding and collecting religion on a Canadian
customer is a data-protection liability, not just noise.

**ID types.** Extend the enum, scoped by country:

| Country | Accepted |
|---|---|
| NG | BVN, NIN, Voter's Card, Driver's Licence, Passport |
| CA | Passport, provincial Driver's Licence, PR Card, Citizenship Card |

Add a `ValidIDTypesForCountry(country string) []IdType` function and validate against it.

**SIN.** If you collect a Canadian SIN at all, store a salted hash plus last 3 digits —
never plaintext, and never in logs. Prefer not collecting it: it is not required for
identity verification and holding it raises the breach cost. Ask the compliance officer
before adding the column.

**Phone numbers.** Replace `AddCountryCodeToPhoneNumber` with E.164 normalisation from
the customer's country using `github.com/nyaruka/phonenumbers`:

```go
func NormalizePhoneNumber(raw, countryISO2 string) (string, error)
```

Update the `uc_customers_phonenumber` unique constraint to operate on the normalised
form, and write a migration that normalises existing rows (they are all `+234`, so this
is mechanical — but assert the count before and after).

`uc_customers_bvn` is `UNIQUE (bvn)` on a nullable column. Postgres allows many NULLs
so Canadian customers with no BVN will not collide — confirm that holds with a test
rather than assuming it.

### 6. `account-created` must carry currency

This is the highest-consequence change in this repo. `postings` consumes
`account-created` and creates the ledger balance row from it. If the event has no
currency, the ledger cannot know what currency the balance is in.

In `internal/models/eventModels.go`, add `currency` to the account-created payload, and
publish one event **per account**, not per customer. Opening a customer's second
currency account must publish its own `account-created`.

Version the event. Add a `schema_version` field and set it to `2`. Coordinate with
`04-seahorse.md` — postings must tolerate both `1` (no currency, treat as NGN) and `2`
during rollout.

### 7. Defects to fix in passing

- `md.Get("x-client-id")[0]` panics when metadata is present but the key is absent.
  Guard with a `len()` check, as seahorse's own server already does.
- `AccountType.String()` indexes a 4-element array with values up to 7.
- CORS is `AllowOrigins: ["*"]` with `ExposeHeaders: ["*"]` on a service holding PII.
  Restrict to known origins.
- `config/config.yml` contains live-looking Twilio and SMTP credentials. **Do not fix
  this inside a multi-currency PR** — raise it separately and rotate them.

---

## Verification

```bash
cd /Users/princeokechukwu/source/repos/swiftbon/swiftbon.account.service
make compose-up && make migrate-up
make test          # must pass with -race

# open a CAD account
curl -s localhost:8183/account/v1/api/customer/account/open \
  -H 'x-client-id: <CLIENT_ID>' -H 'content-type: application/json' \
  -d '{"customer_id":"<id>","currency":"CAD","account_type":2,"product_id":"<id>"}' | jq

# same call again -> same account_number, responseCode "00", no duplicate
# then USD -> a different account_number
# then "XXX" -> responseCode "02"

curl -s localhost:8183/account/v1/api/customer/<id>/accounts -H 'x-client-id: …' | jq
# expect two accounts, currencies CAD and USD
```

Confirm on the bus that opening the USD account published a second `account-created`
carrying `"currency":"USD"` and `"schema_version":2`.

Then check `postings` created a matching balance row — that is the real end-to-end
assertion, and it is the checkpoint gate in `00 §10`.

## Acceptance criteria

- [ ] A customer can hold CAD, USD, NGN, GBP, and EUR accounts simultaneously.
- [ ] Opening the same (owner, product, currency) twice is idempotent.
- [ ] An unsupported or empty currency is rejected with `02`; nothing defaults to NGN.
- [ ] Existing NGN accounts are untouched and still work.
- [ ] A Canadian customer can be onboarded with passport or provincial DL and no BVN.
- [ ] A Nigerian customer can still be onboarded with BVN exactly as before.
- [ ] Phone numbers are E.164-normalised from country; no `+234` literal remains.
- [ ] `account-created` carries `currency` and `schema_version`, one event per account.
- [ ] Unit tests exist for currency validation, idempotent opening, country-scoped ID
      validation, and phone normalisation. There are none today — this is new coverage.

## Out of scope

- Balances and postings — that is `03-postings-service.md`. This service never holds a
  balance.
- KYC provider integration — `06-kyc-service.md`.
- Rotating the committed credentials (raise separately).

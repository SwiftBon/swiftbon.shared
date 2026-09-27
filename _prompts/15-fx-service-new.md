# 15 — `swiftbon.fx.service` (new) — rates, spread, quotes

> Read `00-READ-FIRST-shared-context.md` first. Requires `01-shared-module-new.md`.
> **Must land before `03-postings-service.md` can execute FX.**

---

## Why this is a new service and not part of postings

FX conversion was chosen as in scope. Conversion needs a **rate**, and a rate has
properties that do not belong in a ledger:

- It comes from an external provider on a schedule, and that provider will be
  unavailable sometimes.
- It has a commercial spread that is a pricing decision, changed by business people, not
  a ledger rule.
- It expires. A quote is a short-lived promise to a customer.
- It needs a full audit history — what rate was available when, from whom.

Putting a polling HTTP client, a spread configuration surface, and a rate cache inside
the ledger service would mean a rate provider outage could affect ledger availability,
and a spread change would require a ledger deploy. Keep them apart.

None of the five existing provider services is the right home either.
`swiftbon.juice.service` is the closest — its provider (SpendJuice) has FX-shaped
request fields — but it currently forces both sides to the same currency:

```go
// swiftbon.juice.service/internal/usecase/webapi/initiateOutbound.go:14-15
request.SourceCurrency      = api.config.DefaultCurrency
request.DestinationCurrency = api.config.DefaultCurrency
```

That is a payout rail, not a rate engine.

---

## Prompt

You are working in `/Users/princeokechukwu/source/repos/swiftbon/`.

Create `swiftbon.fx.service` (module `swiftbon.com/fx`), following the estate's
established conventions exactly — copy the structure from
`swiftbon.postings.service`, which is the most complete example:

```
cmd/{app,cronjob,worker}/main.go
config/{config.go,config.yml}
internal/app/app.go                    constructor DI, no framework
internal/controller/http/v1/            Echo v4 + swaggo
internal/controller/grpc/
internal/controller/pubsub/seahorse/
internal/cronjob/                       rate refresh schedule
internal/entity/sql/{query,schema}/     sqlc inputs
internal/usecase/{,repo/fxDb/,webapi/}
internal/{enums,models,middleware,util}/
migrations/  pkg/{httpserver,grpcserver,postgres,logger}/
kubernetes/  scripts/  integration-test/
Dockerfile  Makefile  docker-compose.yml  sqlc.yaml  deploy_locally.sh
```

Use `swiftbon.shared` for `money.Money`, `money.Currency`, and `fx.Quote` — do not
redefine them. Standard config pattern: `cleanenv`, YAML then ENV override, path from
`os.Getenv("CONFIG_LOCATION")`. Standard auth: `x-client-id` header compared against
`config.Auth.ClientId`. Standard envelope: `models.BaseResponse` with the `00`/`02`/`06`/
`98`/`99` codes.

### Responsibilities

1. Fetch mid-market rates for the supported currency pairs on a schedule.
2. Apply configured spread to produce a customer rate.
3. Issue short-lived, single-use quotes.
4. Keep a full audit history of every rate observed and every quote issued.
5. Serve rates and quotes to `postings.service` over gRPC.

It does **not** move money, hold balances, or post to the ledger.

### Currency pairs

Launch currencies: CAD, USD, NGN, GBP, EUR. That is 20 ordered pairs. Store rates
canonically in one direction per unordered pair (alphabetically, e.g. `CAD/USD`) and
derive the inverse, so a pair cannot hold two inconsistent rates.

### Schema

```sql
CREATE SCHEMA IF NOT EXISTS fx_management;

-- every rate observation, append-only
CREATE TABLE fx_management.rate_observations(
    id             uuid PRIMARY KEY DEFAULT UUID_GENERATE_V4(),
    created_at     timestamptz NOT NULL DEFAULT (now()),
    base_currency  CHAR(3)        NOT NULL,
    quote_currency CHAR(3)        NOT NULL,
    mid_rate       NUMERIC(20,10) NOT NULL,
    source         VARCHAR(50)    NOT NULL,
    source_rate_id VARCHAR(255),
    observed_at    timestamptz    NOT NULL,
    CONSTRAINT ck_rate_obs_distinct CHECK (base_currency <> quote_currency)
);
CREATE INDEX ON fx_management.rate_observations (base_currency, quote_currency, observed_at DESC);

-- spread, per pair, with an audit trail of who changed it
CREATE TABLE fx_management.spread_config(
    id             uuid PRIMARY KEY DEFAULT UUID_GENERATE_V4(),
    created_at     timestamptz NOT NULL DEFAULT (now()),
    base_currency  CHAR(3) NOT NULL,
    quote_currency CHAR(3) NOT NULL,
    spread_bps     INTEGER NOT NULL,
    quote_ttl_seconds INTEGER NOT NULL DEFAULT(90),
    max_quote_amount BIGINT,          -- minor units of base; NULL = no cap
    is_active      BOOLEAN NOT NULL DEFAULT(TRUE),
    updated_by     uuid,
    CONSTRAINT ck_spread_bps_sane CHECK (spread_bps >= 0 AND spread_bps <= 10000)
);
CREATE UNIQUE INDEX ON fx_management.spread_config (base_currency, quote_currency)
  WHERE is_active;

-- issued quotes; postings marks them consumed
CREATE TABLE fx_management.quotes(
    id             uuid PRIMARY KEY DEFAULT UUID_GENERATE_V4(),
    created_at     timestamptz NOT NULL DEFAULT (now()),
    sell_currency  CHAR(3)        NOT NULL,
    buy_currency   CHAR(3)        NOT NULL,
    sell_amount    BIGINT         NOT NULL,
    buy_amount     BIGINT         NOT NULL,
    mid_rate       NUMERIC(20,10) NOT NULL,
    rate           NUMERIC(20,10) NOT NULL,
    spread_bps     INTEGER        NOT NULL,
    rate_source    VARCHAR(50)    NOT NULL,
    source_rate_id VARCHAR(255),
    rate_observation_id uuid REFERENCES fx_management.rate_observations(id),
    expires_at     timestamptz    NOT NULL,
    consumed_at    timestamptz,
    consumed_by_reference VARCHAR(450),
    customer_id    uuid,
    CONSTRAINT ck_quotes_distinct CHECK (sell_currency <> buy_currency),
    CONSTRAINT ck_quotes_amounts_positive CHECK (sell_amount > 0 AND buy_amount > 0),
    CONSTRAINT uc_quotes_consumed_reference UNIQUE (consumed_by_reference)
);
CREATE INDEX ON fx_management.quotes (expires_at) WHERE consumed_at IS NULL;
```

`rate_observations` is append-only. Never update a rate row — a new observation is a new
row. This is what makes "what rate was available at 14:32 last Tuesday" answerable.

### Rate providers

Define an interface and at least two implementations, because a single rate source is a
single point of failure for every conversion:

```go
// internal/usecase/webapi/apis/rateProvider.go
type RateProvider interface {
    Name() string
    FetchRates(ctx context.Context, pairs []CurrencyPair) ([]RateObservation, error)
}
```

Note the estate has **no provider abstraction today** — `BaniWebApi`, `PaylonyWebApi`,
`PaystackWebApi`, and `JuiceWebApi` are four bespoke interfaces with incompatible DTOs,
which is why adding a provider means forking a repo. Do not repeat that here. One
interface, N implementations, selected by config.

Implement a primary and a fallback. The specific vendors are a commercial decision —
leave them configurable and note in the README that the choice needs sign-off. Whatever
is chosen must cover NGN, which many providers handle poorly or with a large spread.

**Failure policy — be explicit, this matters:**

- On fetch failure, keep serving the last observation while it is within
  `max_rate_staleness_seconds` (config, default 300).
- Beyond that, **refuse to quote.** Return `06` with a clear message. Do not serve a
  stale rate silently and do not fall back to a hardcoded rate. Quoting a customer a
  rate you cannot honour is worse than declining.
- Emit a `fx-rate-stale` event so it is visible in alerting.

### Cron

`cmd/cronjob` refreshes rates on a schedule (`robfig/cron/v3`, as the estate already
uses). Default every 60s, configurable per pair. Jitter the schedule; do not fire all
pairs simultaneously. Write every successful fetch as a new `rate_observations` row.

### Quote pricing

```
mid   = latest mid_rate for the pair (inverted if needed)
rate  = mid × (1 − spread_bps/10000)     when selling base for quote
```

Round the customer rate to 10 decimal places. Compute the buy amount with
`money.Convert` from the shared module, so the rounding rule and the residual are
identical to what postings will apply — a mismatch here means postings computes a
different buy amount than the customer was quoted.

Reject a quote when: the pair has no active spread config, no fresh observation,
`sell_amount` exceeds `max_quote_amount`, or the currencies are equal or unsupported.

### API

**`POST /fx/v1/api/quote`**
```json
{ "sell_currency": "CAD", "buy_currency": "USD", "sell_amount": 5000, "customer_id": "…" }
```
```json
{ "responseCode": "00", "responseDetails": {
  "quote_id": "…", "sell": {"amount":5000,"currency":"CAD"},
  "buy": {"amount":3650,"currency":"USD"},
  "rate": "0.7300000000", "mid_rate": "0.7336000000",
  "spread_bps": 49, "expires_at": "2026-08-20T10:01:30Z" }}
```

Support the reverse direction too — a customer asking "how much CAD to get USD 100"
needs `buy_amount` as the input. Accept exactly one of `sell_amount` or `buy_amount` and
reject if both or neither are present.

**`GET  /fx/v1/api/quote/:id`** — fetch, including consumed/expired state.
**`POST /fx/v1/api/quote/:id/consume`** — mark consumed with a reference. Idempotent:
consuming with the same reference twice succeeds; with a different reference fails.
**`GET  /fx/v1/api/rates`** — current customer rates for all pairs. Indicative only;
label it as such in the response.
**`GET  /fx/v1/api/rates/:base/:quote/history?from=&to=`** — audit endpoint.

gRPC mirror for `postings`: `Quote`, `GetQuote`, `ConsumeQuote`, `GetRates`. Define it in
`swiftbon.shared/proto/fx/v1/fx.proto`.

### Consumption is a two-phase handshake

Postings must not be able to double-spend a quote, and must not lose one if it crashes
between calls.

- `ConsumeQuote(quote_id, reference)` sets `consumed_at` and `consumed_by_reference`
  atomically, conditional on `consumed_at IS NULL`.
- Idempotent on the same reference so a postings retry succeeds.
- Postings calls it **inside** its posting transaction. If the posting rolls back, the
  quote stays consumed — that is the safe direction. An unusable consumed quote costs a
  re-quote; a reusable consumed quote costs money. Document this choice.

Add a worker that expires unconsumed quotes past `expires_at`.

### Config

```yaml
fx:
  primary_provider:   'provider-a'
  fallback_provider:  'provider-b'
  max_rate_staleness_seconds: 300
  refresh_interval_seconds:   60
  default_spread_bps:  50
  default_quote_ttl_seconds: 90
  pairs: ['CAD/USD','CAD/NGN','CAD/GBP','CAD/EUR','USD/NGN','USD/GBP','USD/EUR','GBP/EUR','GBP/NGN','EUR/NGN']
```

Env: `FX_PRIMARY_PROVIDER`, `FX_FALLBACK_PROVIDER`, `FX_MAX_RATE_STALENESS_SECONDS`,
`FX_REFRESH_INTERVAL_SECONDS`, `FX_DEFAULT_SPREAD_BPS`, `FX_DEFAULT_QUOTE_TTL_SECONDS`,
plus `<PROVIDER>_HOST` / `<PROVIDER>_API_KEY` per provider.

**Provider credentials go in the deployment secret store, never in `config.yml`.** Every
existing repo has live-looking API keys committed to `config/config.yml` and
`.env.example`. Do not continue that pattern in a new service.

### Ports

Existing assignments: postings 8103/8203, account 8183/8193, kyc 8105/8205,
communication 8084/8094, billers 8103/8203. Pick unused ones — suggest HTTP `8107`,
gRPC `8207`, seahorse consumer `8407` — and confirm against every repo's `config.yml`
before committing.

### Events

Publish via seahorse: `fx-rate-updated`, `fx-rate-stale`, `fx-quote-issued`,
`fx-quote-consumed`, `fx-quote-expired`. Register the consumer group `fx.rates`.

### Testing

- Rate math: spread application, inversion, cross-rate derivation, 10-dp rounding.
- Quote lifecycle: issue → consume → double-consume rejected → expiry.
- Staleness: with the clock advanced past `max_rate_staleness_seconds`, quoting fails
  with `06` and does not serve a stale rate.
- Provider failover: primary errors, fallback serves, an observation is written with the
  fallback's `source`.
- Consistency with `money.Convert`: for a table of amounts and rates, the service's
  `buy_amount` equals what postings computes. Assert this directly — it is the seam
  where a customer could be quoted one number and posted another.
- Concurrency: two simultaneous `ConsumeQuote` calls on one quote; exactly one succeeds.

### Acceptance criteria

- [ ] Rates refresh on schedule and land as append-only observations.
- [ ] A quote returns sell, buy, rate, mid rate, spread, and expiry.
- [ ] Quotes are single-use; concurrent consume yields exactly one winner.
- [ ] Expired quotes are rejected, never silently re-quoted.
- [ ] With rates stale beyond the threshold, quoting fails explicitly.
- [ ] Provider failover works and is visible in the observation's `source`.
- [ ] `buy_amount` matches `money.Convert` exactly, proven by test.
- [ ] Full rate and quote history is queryable for audit.
- [ ] No credentials in `config.yml` or `.env.example`.
- [ ] Rate history is never mutated or deleted.

### Out of scope

- Hedging, netting, or managing the FX position that accumulates in
  `FX_POSITION_<CCY>`. Treasury will need a process for this — flag it as a known gap
  with real financial exposure, since an unhedged position is a market risk the MSB
  carries. It is not a code change in this prompt.
- Customer-specific or tiered pricing. Spread is per pair for now; note the extension
  point.

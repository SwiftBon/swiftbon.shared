# 14 — `swiftbon.com.backend.go.template` — refresh the scaffold

> Read `00-READ-FIRST-shared-context.md` first.
> **Do this last.** It should codify what the other prompts actually produced, not
> predict it.

---

## Prompt

You are working in
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.com.backend.go.template`.

This is meant to be the canonical scaffold new Swiftbon services are forked from. Right
now it is not — it is a stale fork of the communication service, and every service forked
from it inherits that staleness. Since Swiftbon is a Canadian MSB now building
multi-currency services, the next service forked from this template should start
multi-currency-ready.

### Current state (verified)

**It is not a clean scaffold.** It is a fork of the communication service:

- `go.mod` says `module swiftbon.com/accountms` — the same module path as
  `swiftbon.account.service`.
- It contains working **SMS and email** code: `internal/usecase/{sms,email}.go`,
  `internal/usecase/webapi/{sms_twilio.go,smtpHandler.go}`, routes
  `POST /v1/api/sms`, `POST /v1/api/email`, `GET /v1/api/sms`, `GET /v1/api/email`.
- Migrations create `sms_messages` and `email_messages`.
- `config/config.yml:22-33` has live-looking Twilio credentials and the SMTP password
  `Princess4Daprinz`; `kubernetes/secrets.yml:7-17` has them base64-encoded.

**It is behind the services that forked from it:**

| Aspect | Template | Current services |
|---|---|---|
| Config path | hardcoded `./config/config.yml` | `os.Getenv("CONFIG_LOCATION")` |
| DB driver | `database/sql` + `lib/pq` | `pgx/v5` + `pgtype` |
| DSN | discrete `DB_*` parts | single `PG_URL` |
| sqlc | `sqlc.json` v1 | `sqlc.yaml` v2, `sql_package: pgx/v5` |
| Validation | none | `go-playground/validator` at the controller |
| Event bus | Kafka (`pkg/kafka`) | seahorse gRPC pub/sub |
| Go directive | 1.21.4 | 1.24.1 |

**`pkg/postgres/postgres.go` panics on every failure** (lines 49, 60, 68, 78, 83, 90, 95)
and `maxPoolSize` is stored but never applied to the `*sql.DB`.

**Kafka topics do not match.** The enums produce `"SendSMS"`/`"SendEmail"`
(`pkg/kafka/consumer_topics.go:14`) while `config/config.yml:41-43` subscribes to
`sendemail`/`sendsms`. The consumer handlers are stubs returning `nil`.

**`internal/app/app.go:48-57` calls `repo.New(pg)` and `webapi.New(cfg)` twice**, creating
duplicate instances.

**`.golangci.yml` disables all linters then enables ~60**, including several removed from
modern golangci-lint (`deadcode`, `varcheck`, `structcheck`). The template's own code
would not pass it.

**CI pins Go 1.17** (`ci.yml:63`) against a `go 1.21.4` module, so it cannot build.

**The integration test sends real SMS and email** to `+2348160273875` and
`okechukwuprince@hotmail.com` (`integration-test/integration_test.go`).

**The README is verbatim upstream `go-clean-template`**, titled "Persian Black AccountMs
Service" and describing an SMS/email service, including its `docs/img/*` diagrams.

---

## Tasks

### 1. Strip it back to a scaffold

Remove everything domain-specific. A template that ships a working SMS service teaches
every new service to carry SMS code it does not need.

Delete: `internal/usecase/{sms,email}.go`, `internal/usecase/webapi/{sms_twilio.go,smtpHandler.go}`,
the SMS/email routes and handlers, the `sms_messages` and `email_messages` migrations, the
Twilio and SMTP config blocks, and the SMS/email integration tests.

Replace with **one** minimal example resource — a `widgets` table with CRUD — that
demonstrates the full vertical slice (route → validation → usecase → sqlc repo →
migration → test) without implying any domain.

Change the module path to something unmistakably a template:
`module swiftbon.com/servicetemplate`. Sharing a module path with
`swiftbon.account.service` is actively confusing.

### 2. Bring it up to current practice

Apply everything the real services have moved to:

- `os.Getenv("CONFIG_LOCATION")` for the config path.
- `pgx/v5` + `pgtype`, single `PG_URL`, `sqlc.yaml` v2 with `sql_package: "pgx/v5"`.
- `go-playground/validator` at the controller boundary — with a **checked** type
  assertion. Every service currently does `err.(validator.ValidationErrors)` unchecked
  (e.g. `billers/internal/controller/http/v1/billers.go:143-155`), which panics on a
  non-validation error. The template is where that gets fixed once.
- Go 1.24.1, and fix CI's Go 1.17 pin.
- **Delete `pkg/kafka` and `pkg/rabbitmq`.** Both are dead in every service. Carrying
  them forward means every new service inherits two unused messaging stacks.
- Replace them with a **seahorse client**: register, publish, consume, mark-processed.
  This is currently re-implemented verbatim in every repo and is the single
  highest-value thing a template can provide.
- Include the guarded `x-client-id` metadata read. Every service has
  `md.Get("x-client-id")[0]`, which panics when metadata is present without that key;
  seahorse's own server has the correct `len()`-checked version. Ship the correct one.

### 3. Fix the infrastructure defects

- `pkg/postgres/postgres.go`: return errors instead of panicking, and actually apply
  `maxPoolSize` to the pool.
- `internal/app/app.go`: construct `repo` and `webapi` once.
- `pkg/logger/logger.go`: honour `cfg.Level` instead of hardcoding `TraceLevel`.
- `internal/middleware/trackResponse.go`: remove the
  `persian.black.devtroy.auth.service` log tag; make the service name a config value.
- Converge auth on `x-client-id` (the template and communication server use `Client_Id`).
- CORS: default to a configured allow-list, not `AllowOrigins: ["*"]` with
  `ExposeHeaders: ["*"]`.
- `.golangci.yml`: prune to linters that exist and that the template's own code passes.
  A lint config nothing passes gets ignored, which is worse than a smaller one enforced.
- **Remove the committed Twilio and SMTP credentials** from `config/config.yml` and
  `kubernetes/secrets.yml`. In a template these get copied into every new repo — which is
  demonstrably how they ended up in all 14. Replace with placeholders and a README note
  pointing at the deployment secret store.
- Delete `kubernetes/`. It is dead legacy (Dapr sidecars, `ghcr.io/persianblack` images,
  GKE ingress) and deployment is rsync + systemd. Replace it with the `scripts/*.service`
  and `deploy_locally.sh` pattern the real services use.

### 4. Make it multi-currency-ready

This is why the template is in this prompt set.

- Add `swiftbon.shared` as a dependency with the `replace` directive from prompt 01.
- Include a worked example of a money-carrying endpoint: a DTO with `money.Money`, a
  `CHAR(3)` currency column with a check constraint, validation via `money.Lookup`, and a
  test asserting that an unsupported currency is rejected.
- Include the three-migration pattern (add nullable → backfill → enforce `NOT NULL`) as a
  commented example, since every service in this project needs it and getting it wrong
  means a failed deploy.
- Add a `docs/CONVENTIONS.md` capturing the decisions from `00-READ-FIRST`:
  amounts are `int64` minor units, currency is ISO 4217 validated against the registry,
  rates are `NUMERIC(20,10)` and `decimal.Decimal`, never float; currency is never
  defaulted; per-currency balance is enforced in double entry.

### 5. Provide the missing test scaffolding

The estate has **no unit tests** — only one network-level integration test per repo,
several stale (the account service's test hits `http://app:8183/v1` while the router
mounts `account/v1`). The template is why: it ships no unit-test example, so nobody
writes one.

- Add unit test examples: a usecase test with a mocked repo, a controller test with a
  mocked usecase, a currency-validation test.
- Resolve the mock tooling contradiction: the `Makefile` target says `mockery`, the
  sources carry `//go:generate mockgen` directives, and **neither has been run — no mock
  files exist anywhere**. Pick one, wire it, commit generated mocks for the example.
- Make the integration test hermetic. **It currently sends real SMS and email to a real
  phone number and a real inbox.** A template must not teach that.
- `make test` runs `-race` with coverage and must be non-vacuous.

### 6. Document what it is

Replace the upstream `go-clean-template` README. It is copied verbatim into
`communication`, `chatbot`, `seahorse`, `kyc`, `account`, and `postings`, all titled
"Persian Black AccountMs Service" and all describing an SMS/email service none of them
are. That single stale file is the most-duplicated wrong document in the estate.

Write: what the template is, how to fork it (the rename checklist — module path, sqlc
package, schema file, route prefix, log file, consumer group, ports, systemd unit, CD
target), the port-allocation table so new services do not collide (billers and postings
both claim 8103/8203 today), and a pointer to `docs/CONVENTIONS.md`.

---

## Verification

```bash
cd /Users/princeokechukwu/source/repos/swiftbon/swiftbon.com.backend.go.template
go build ./... && make test        # -race, non-vacuous
make lint                          # must pass its own config
make compose-up && make migrate-up
curl -s localhost:<port>/healthz
```

- The example money endpoint rejects `"XYZ"` and accepts `"CAD"`.
- Integration tests pass with no external network calls and send nothing to a real
  recipient.
- `grep -rn "twilio\|smtp\|Princess4Daprinz" .` returns nothing.
- `grep -rn "sms\|email" --include='*.go' .` returns nothing outside comments.

**The real test:** fork the template following the README checklist and stand up a
throwaway service. Time it. If the checklist is incomplete you will find out immediately,
and that is the whole value of this prompt.

## Acceptance criteria

- [ ] No SMS, email, Twilio, or SMTP code or config remains.
- [ ] No credentials in `config.yml`, `.env.example`, or `kubernetes/`.
- [ ] Module path is unmistakably a template and does not collide with a real service.
- [ ] `pgx/v5`, `PG_URL`, `sqlc.yaml` v2, `CONFIG_LOCATION`, Go 1.24.1, CI matching.
- [ ] `pkg/kafka` and `pkg/rabbitmq` deleted; a seahorse client provided.
- [ ] Guarded `x-client-id` read; postgres returns errors and applies the pool size;
      logger honours config level.
- [ ] `swiftbon.shared` wired with a worked money endpoint and the three-migration pattern.
- [ ] `docs/CONVENTIONS.md` records the money and currency rules.
- [ ] Unit test examples plus committed mocks; one mock tool, wired.
- [ ] Integration tests are hermetic.
- [ ] `.golangci.yml` passes against the template's own code.
- [ ] README describes the template and includes the fork checklist and port table.
- [ ] A test fork produces a running service by following the README alone.

## Out of scope

- Retrofitting the existing 14 services onto the refreshed template. They are being
  updated by their own prompts; converging them on a shared scaffold is a separate,
  larger effort worth proposing afterwards.
- Extracting `pkg/*` into `swiftbon.shared`. Prompt 01 deliberately keeps that module
  small. Once the template stabilises, that extraction becomes the obvious next step —
  note it.

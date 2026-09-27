# 16 — `swiftbon.go.deployment.template` — deploy the new services and their secrets

> Read `00-READ-FIRST-shared-context.md` first.
> Do this alongside the services it deploys — `15-fx-service-new.md` needs it to ship.

---

## Prompt

You are working in
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.go.deployment.template`.

Swiftbon is a Canadian licensed MSB. The multi-currency work adds a new service
(`swiftbon.fx.service`), a new shared Go module (`swiftbon.shared`), new databases, new
config, and new secrets. This repo holds the deployment conventions those need.

### Current state (verified)

**This is not a Swiftbon repo.** `go.mod` says
`module github.com/joefazee/autodeploy` — it is an imported tutorial ("How to deploy your
Go application using Github Actions", `readme.md:1`), built on Gin + viper + asynq, and
unrelated to the Swiftbon service stack.

**But its pattern is what production actually runs.** There is no Helm, no Terraform, no
Kubernetes, and no container registry, despite every service carrying a `kubernetes/`
directory. Those directories are dead legacy — Dapr sidecar annotations,
`ghcr.io/persianblack` images, GKE ingress. The live path is:

`.github/workflows/deployment.yml` — GitHub Actions → `make audit` → `make build` →
`rsync` the binary and migrations over SSH → run `migrate` on the host → `sudo mv` the
systemd unit → `systemctl enable/restart` → reload Caddy.

**Secrets convention:** GitHub Actions `vars.*` for non-sensitive, `secrets.*` for
sensitive. The `.env` is generated **on the host** by `echo`-appending lines
(`deployment.yml:57-74`) and loaded by systemd `EnvironmentFile=`.

**Runtime:** one systemd unit per process. `scripts/api.service` — `Type=exec`,
`Restart=on-failure`, `RestartSec=5`, `StartLimitIntervalSec=600`/`Burst=5`. Real
services add caps: `swiftbon.communication.server/scripts/communication-app.service` has
`MemoryMax=500M`, `CPUQuota=15%`.

**Ingress:** Caddy, `reverse_proxy localhost:3000`, auto-TLS.

**Observability:** `scripts/alloy.config` — Grafana Alloy tails `/tmp/alloy-logs/*.log`,
parses the logrus JSON via `stage.json`, promotes `level`/`msg` to Loki labels, pushes to
`localhost:3100`. This is why every service logs JSON to a file rather than stdout.

**`scripts/run.sh` documents the real topology** — and leaks every database password in
plaintext (lines 36-99, commented-out Postgres provisioning):

| Database | Role |
|---|---|
| `swiftbon_cba_postings` | `swiftbon_cba_postings_user_new` |
| `swiftbon_cba_account` | `swiftbon_cba_account_user_new` |
| `swiftbon_kyc` | `swiftbon_kyc_user_new` |
| `swiftbon_paystack` | `swiftbon_paystack_user_new` |
| `swiftbon_juice` | `swiftbon_juice_user_new` |
| `seahorse` | `seahorse_user_new` |
| `communication` | `communication_user_new` |

Database-per-service on managed DigitalOcean Postgres (`sslmode=require`), each with
`uuid-ossp` and `pgcrypto`. `run.sh` also sets timezone `Africa/Lagos`, creates a
`devops` user, configures `ufw` and `fail2ban`, installs `migrate` v4.14.1 and Caddy, and
builds `dlib` from source for KYC face matching.

Per-service CD lives in each repo (`cd.yml` for `develop` with `vars.DEVELOP_*`,
`cd-prod.yml` for `main` with `vars.PROD_*`). Deploy target is
`~/bin/<service>/{app,consumer,migrations/,config/config.yml,.env,custom}`. API host is
`https://dev.api.swiftbon.com/<service>`.

---

## Tasks

### 1. Provision `swiftbon.fx.service`

Add to the topology, following the established per-service pattern:

- Database `swiftbon_fx` with role `swiftbon_fx_user`, extensions `uuid-ossp` and
  `pgcrypto`. Add it to `run.sh`'s provisioning block — **without a plaintext password**
  (see task 4).
- systemd units: `fx-app.service`, `fx-worker.service`, `fx-cronjob.service`, following
  `communication-app.service` including `MemoryMax` and `CPUQuota`.
- Caddy route for `https://dev.api.swiftbon.com/fx`.
- Deploy target `~/bin/fx/`.
- Alloy log path for `fx.log`.
- A `cd.yml` / `cd-prod.yml` pair matching the estate's shape, building `cmd/app`,
  `cmd/worker`, and `cmd/cronjob`.

**Port allocation.** Existing: postings 8103/8203, account 8183/8193, kyc 8105/8205
(+webhook 8106), communication 8084/8094, billers 8103/8203 (**collides with postings**),
juice/paystack/paylony/bani each with app + webhook + consumer ports, seahorse 8093.

Add a **port allocation table** to this repo as the single source of truth, resolve the
billers/postings collision, and assign fx (suggest HTTP 8107, gRPC 8207, consumer 8407).
Verify every assignment against every repo's `config.yml` before committing — the
collision above shows this is not currently checked anywhere.

### 2. Handle the `swiftbon.shared` module in CI/CD

`swiftbon.shared` is consumed via a `replace` directive pointing at a **sibling
directory** (`replace swiftbon.com/shared => ../swiftbon.shared`). That works locally and
**breaks in CI**, where each repo is checked out alone and the sibling does not exist.

Resolve it explicitly. Options, in order of preference:

1. **Publish to a private Go module registry** (or a private GitHub repo with
   `GOPRIVATE` and a deploy key). Cleanest; versioned; the `replace` directive goes away.
2. **Check out `swiftbon.shared` as a sibling in CI** — add a second
   `actions/checkout` with `path: ../swiftbon.shared`. Works, but every workflow in every
   repo needs it and version skew becomes invisible.
3. **Vendor it.** Reproducible, but 15 copies to update — which is the problem the shared
   module exists to solve.

Pick option 1 unless there is a reason not to, document the choice here, and update the
workflow template. **Do not leave this unresolved** — every repo's build breaks the moment
it adopts the module, and that will look like a mysterious CI failure rather than a known
decision.

### 3. New config and secrets

The multi-currency work introduces these. Add them to the workflow template and document
which are `vars.*` and which are `secrets.*`:

**FX service (new):** `FX_PRIMARY_PROVIDER`, `FX_FALLBACK_PROVIDER`,
`FX_MAX_RATE_STALENESS_SECONDS`, `FX_REFRESH_INTERVAL_SECONDS`, `FX_DEFAULT_SPREAD_BPS`,
`FX_DEFAULT_QUOTE_TTL_SECONDS` (all `vars`), plus `<PROVIDER>_HOST` (`vars`) and
`<PROVIDER>_API_KEY` (**`secrets`**).

**Postings:** `FX_SERVER`, `FX_SERVER_CLIENT_ID`.

**Across services:** `SUPPORTED_CURRENCIES`. Note that
`STAMP_DUTY_THRESHOLD`, `STAMP_DUTY_AMOUNT`, and `STAMP_DUTY_ACCOUNT_NUMBER` move from
config into the `levy_rules` table (prompt 03) — remove them from the workflow once
postings ships, and note the ordering so a deploy does not fail on a missing required var.

**KYC:** the Canadian provider's host and API key (prompt 06).

Watch for `env-required:"true"` fields with no YAML default — `billers` has
`Postings{Host,ClientId}` required with no `postings:` block in `config.yml`, so it only
boots because CD injects them. Any new required var must be either in the workflow or in
the YAML, and it is worth adding a CI check that asserts that.

### 4. Stop shipping secrets in the repo

Every repo has live-looking credentials committed. Concentrated examples:

- `scripts/run.sh` (this repo) — every database password in plaintext.
- `swiftbon.paylony.service/config/config.yml:80-82` — `sk_test_…`, `pk_test_…`,
  `encryption_key`.
- `.env.example` in paylony, paystack, and juice — `…_ACCESS_TOKEN=sk_live_…`.
- `swiftbon.communication.server/config/config.yml:22-33` and the backend template —
  Twilio credentials, SMTP password `Princess4Daprinz`.
- `swiftbon.chatbot.whatsapp.service/.env.example` — WhatsApp permanent token, Cloudinary
  secret, MongoDB Atlas URI, an encrypted RSA private key; plus a committed
  `config/private.pem`.
- `swiftbon.communication.server/.env.example:2` — DigitalOcean managed-Postgres DSN with
  password.

**This is the highest-severity issue in the estate and it is independent of
multi-currency.** For a licensed MSB it is also a regulatory exposure, not just an
engineering one.

In this repo, do the parts that belong here:

- Remove the plaintext passwords from `run.sh`; read them from the environment or a
  secret store at provisioning time.
- Add a **secret inventory** document: every secret, which service uses it, where it
  lives, and its rotation owner.
- Add a CI check to the workflow template that fails on a likely credential in a
  committed file — `gitleaks` or `trufflehog` as a required job. This is the control that
  stops it recurring.
- Write the rotation runbook.

**Raise the rotation itself as its own tracked work item.** Do not fold it into a
multi-currency PR — it touches every repo, needs coordination with live providers, and
would make the currency changes unreviewable. But do raise it explicitly, with the list
above, rather than leaving it as a note in a prompt.

### 5. Fix the CI Go version

Every repo's `ci.yml` pins **Go 1.17** (`ci.yml:63`) against modules declaring
`go 1.24.1`, `1.25.2`, `1.23.5`, or `1.22.2`. The `tests` job cannot pass anywhere in the
estate — which is a large part of why there are no unit tests.

Fix it in the workflow template and note that each repo needs it applied. Since the
multi-currency prompts all require tests, this must be fixed for any of them to be
verifiable in CI.

### 6. Database migration safety for the money path

The postings changes rewrite **every balance row's checksum** (prompt 03 task 2). That is
a data-rewriting migration on the ledger, and it needs more care than the current
`migrate -path=./migrations -database="$DB_DSN" up` in a deploy step.

Add to the runbook:

- **A verified backup immediately before**, with a documented restore test — not just a
  snapshot assumed to work.
- A pre-migration row count and a post-migration verification query asserting every
  checksum validates under the new preimage.
- A rehearsal against a production-sized copy, with the elapsed time recorded, so the
  maintenance window is known rather than guessed.
- A tested rollback: the `down` migration must restore the old preimage and revalidate.
- Explicit confirmation that the deploy sequence matches prompt 04 §5 — seahorse first,
  then postings tolerating both event versions, then account publishing v2. Deploying
  account before postings produces NGN-labelled CAD balances silently.

Consider gating money-path migrations behind a manual approval step rather than running
them automatically on push to `main`, which is the current behaviour.

### 7. Housekeeping

- The module path `github.com/joefazee/autodeploy` and the Gin/viper/asynq tutorial code
  are confusing in a Swiftbon repo. Either rename and strip it to just the deployment
  assets, or clearly document that only `scripts/`, `.github/workflows/`, and `plan.md`
  are canonical and the Go code is a vestigial sample.
- `run.sh` sets timezone `Africa/Lagos`. For a Canadian MSB, hosts should be UTC — logs,
  financial dates, and the 24-hour aggregation window (prompt 03 task 7) all depend on it,
  and a Lagos-local host makes a Canadian reporting day ambiguous. Change to UTC and
  confirm nothing depends on local time.
- Remove the dead `kubernetes/` directories from the service repos, or document clearly
  that they are unused. Right now they read as the deployment method and are not.
- `deployment.yml` writes `ENVIRONMENT=development` unconditionally
  (`deployment.yml:57-74`) even in the production path. Verify the per-service
  `cd-prod.yml` does not inherit that.

---

## Verification

- Provision a staging host from `run.sh` with no plaintext secrets in the script; confirm
  it comes up.
- Deploy `swiftbon.fx.service` end to end via its `cd.yml`; confirm the systemd units
  start, Caddy routes `/fx`, migrations ran, and logs reach Loki.
- Confirm no port collision: for every service, assert its configured ports against the
  allocation table programmatically.
- Build a repo that consumes `swiftbon.shared` **in CI** and confirm the chosen resolution
  from task 2 works from a clean checkout.
- Run the secret-scanning job against the current repos and confirm it **fails** — it
  should, and that failure is the baseline for the rotation work.
- Rehearse the postings checksum migration against a production-sized copy: record the
  duration, verify every checksum, then roll back and verify again.
- Confirm CI runs the correct Go version and that `make test` executes non-vacuously.

## Acceptance criteria

- [ ] `swiftbon.fx.service` fully provisioned: database, units, Caddy route, CD workflow,
      log shipping.
- [ ] Port allocation table exists, is authoritative, and the billers/postings collision
      is resolved.
- [ ] `swiftbon.shared` resolution in CI is decided, documented, and proven from a clean
      checkout.
- [ ] All new env vars documented and split correctly between `vars` and `secrets`.
- [ ] No plaintext credentials in `run.sh`.
- [ ] Secret inventory and rotation runbook written; rotation raised as tracked work.
- [ ] Secret-scanning is a required CI job.
- [ ] CI Go version matches the modules.
- [ ] Money-path migration runbook covers verified backup, rehearsal with timing,
      post-migration checksum verification, and tested rollback.
- [ ] Deploy ordering for the event-contract changes is documented and enforced.
- [ ] Hosts are UTC.

## Out of scope

- Migrating to Kubernetes or containerised production. The rsync + systemd path works;
  changing it is a separate decision.
- Performing the credential rotation (raise it; do not do it inside this work).
- Retrofitting the deployment pattern onto the 14 repos beyond the changes named here.

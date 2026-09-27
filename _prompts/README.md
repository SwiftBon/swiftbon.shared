# Swiftbon multi-currency + transaction processing — implementation prompts

One prompt per repo, plus two new components. Each prompt is self-contained enough to
paste into a fresh Claude Code session opened in that repo.

**Start with [`00-READ-FIRST-shared-context.md`](00-READ-FIRST-shared-context.md).** It
holds the money model, the currency registry, the FX design, the ledger invariants, and
the architectural facts every other prompt assumes. Read it before any of them, and give
it to whichever session you run.

---

## Goal

Swiftbon is a Canadian licensed MSB running on a codebase built for a single-currency
Nigerian retail-banking product. The objective:

1. Open accounts for customers in **any supported currency**.
2. Process transactions for them, including **cross-currency**.

## Decisions already made

| Decision | Choice |
|---|---|
| Multi-currency model | Account per currency |
| Launch currencies | CAD, USD, NGN, GBP, EUR |
| FX | In scope — quote, lock, convert via FX position accounts |
| Ledger atomicity | Fix it — one transactional double-entry primitive |

---

## The prompts, in execution order

Dependencies are real. This order is not a suggestion.

| # | Prompt | Repo | Scope |
|---|---|---|---|
| **01** | [shared-module-new](01-shared-module-new.md) | **new** `swiftbon.shared` | Money type, currency registry, FX types, protos. **Foundation — run first.** |
| **15** | [fx-service-new](15-fx-service-new.md) | **new** `swiftbon.fx.service` | Rate providers, spread, single-use quotes, audit history |
| **02** | [account-service](02-account-service.md) | `swiftbon.account.service` | Currency on accounts, account-opening API, Canadian onboarding |
| **03** | [postings-service](03-postings-service.md) | `swiftbon.postings.service` | **Currency on the ledger, atomicity fix, FX postings.** The critical one. |
| **04** | [seahorse](04-seahorse.md) | `seahorse` | Versioned event contracts, delivery correctness |
| — | **CHECKPOINT** | — | **Verify end-to-end before continuing. See below.** |
| **05** | [juice-service](05-juice-service.md) | `swiftbon.juice.service` | Multi-currency payout rail. Reference implementation. |
| **06** | [kyc-service](06-kyc-service.md) | `swiftbon.kyc.service` | Jurisdiction-routed identity verification |
| **07** | [paystack-service](07-paystack-service.md) | `swiftbon.paystack.service` | Currency-explicit NGN rail + **fix stranded funds** |
| **08** | [paylony-service](08-paylony-service.md) | `swiftbon.paylony.service` | **Fix two money-precision bugs** + currency-explicit |
| **09** | [bani-service](09-bani-service.md) | `swiftbon.bani.service` | **Decide whether to keep it first** |
| **10** | [billers-service](10-billers-service.md) | `swiftbon.billers.service` | Bill currency vs. funding currency, per-currency GLs |
| **11** | [flights-service](11-flights-service.md) | `swiftbon.flights.service` | **Re-fork from billers — do not patch** |
| **12** | [communication-server](12-communication-server.md) | `swiftbon.communication.server` | Currency-correct alerts (currently prints `₦` on everything) |
| **13** | [chatbot-whatsapp-service](13-chatbot-whatsapp-service.md) | `swiftbon.chatbot.whatsapp.service` | Currency selection and display; fix deployability |
| **14** | [backend-go-template](14-backend-go-template.md) | `swiftbon.com.backend.go.template` | Refresh the scaffold. **Do last.** |
| **16** | [deployment-template](16-deployment-template.md) | `swiftbon.go.deployment.template` | New service provisioning, secrets, CI |

Prompts 05–09 can run in parallel once the checkpoint passes. 10 depends on 03 and 15.
11 depends on 10. 12 depends on 03. 16 should run alongside 15.

### The checkpoint

Do not start prompt 05 until you can, on a local stack: open a CAD and a USD account for
one customer, fund the CAD account, quote CAD→USD, execute the conversion, and see both
balances plus four balanced ledger legs with the rate recorded. Exact commands and
assertions are in [`03-postings-service.md`](03-postings-service.md) § Verification.

---

## What the exploration found

Grounding for the prompts, verified by direct inspection.

**All 14 repos are Go**, forked from the same `go-clean-template` scaffold: Echo v4,
gRPC, `pgx/v5`, sqlc, `golang-migrate`, `cleanenv`, logrus. Identical directory layout.
Database-per-service. Deployment is GitHub Actions → rsync → systemd, **not** Kubernetes
despite the `kubernetes/` directories.

**`seahorse` is the event bus** — a homegrown Postgres-backed pub/sub broker with gRPC
push delivery, at-least-once. Kafka and RabbitMQ exist in every repo's `pkg/` and are
dead code.

**There is no shared Go module.** Zero `replace` directives, zero cross-repo imports.
`pkg/`, `internal/models`, `internal/enums`, middleware, and even `.proto` files are
copy-pasted per repo and have already drifted — `account.service` and `postings.service`
disagree on `AccountType` for the same integer. This is why prompt 01 exists.

**The core money path has no currency concept at all.**
`swiftbon.account.service` and `swiftbon.postings.service` contain **zero** occurrences
of `currency`, `NGN`, `CAD`, or `USD` in any Go file, SQL file, migration, or proto.
Currency is implicit and single-valued. It must be *added*, not corrected.

NGN is assumed in four concrete places: the ledger's one-balance-per-account schema,
Nigerian CBN tier limits seeded as raw kobo integers, stamp duty hardcoded in postings
config, and `"+234" + phonenumber` string concatenation.

### Money-path defects found along the way

These are real, verified, and sit under live money movement. Each is assigned to a prompt.

1. **`/transfer/single` can destroy money.** `processTransfer.go` does its debit and
   credit as separate non-transactional autocommitted writes with no row lock, no
   `credits == debits` check, and ledger history in 8 fire-and-forget goroutines. If the
   credit fails after the debit succeeds, the funds are gone with no compensating entry.
   `/transfer/post` in the same service is correctly transactional — the fix is to route
   everything through it. → **prompt 03**

2. **Paylony truncates every payout.** `Amount: fmt.Sprintf("%d", transaction.Amount/100)`
   — integer division, so ₦105.24 is sent as ₦105 while the ledger is debited the full
   amount. → **prompt 08**

3. **Paylony round-trips collections through a float**, with an invalid `ParseFloat`
   bitSize and a truncating `int64(f*100)` that turns `1.15` into `114` instead of `115`.
   A `TODO` in the same function admits nobody knows whether the provider's amount is
   naira or kobo. → **prompt 08**

4. **Paystack and Paylony strand customer funds.** Both debit via postings *before*
   calling the provider, and both ship with `//TODO: fire event to reverse funds`. A
   failed payout leaves the customer debited with no reversal and no status polling.
   → **prompts 07, 08**

5. **Three webhook endpoints accept unauthenticated money events.** Juice has a correct
   HMAC verifier that is **commented out**; Paystack and Paylony have no verification at
   all; none of the three webhook route groups apply auth middleware. → **prompts 05, 07, 08**

6. **Bani's `InitateOutbound` is a copy-pasted Twilio SMS sender.** → **prompt 09**

7. **Four services, four representations of an amount:** `int64` (juice, paystack),
   `string` (paylony), `float64` (bani). → **prompt 01**

8. **`md.Get("x-client-id")[0]` panics** on metadata without that key, in every gRPC
   handler in every repo. Seahorse's own server has the correct guarded version.

9. **Live-looking credentials are committed** to `config/config.yml` and `.env.example`
   in every repo — including an `sk_live_…` token, a WhatsApp permanent token, a
   DigitalOcean Postgres DSN, an RSA private key, and every database password in
   `run.sh`. **This is the highest-severity finding and it is independent of this
   project.** → raised in **prompt 16**, to be rotated as separate tracked work.

10. **CI pins Go 1.17** against modules declaring 1.22–1.25, so the test job cannot pass
    anywhere. Combined with **zero unit tests estate-wide**, nothing is currently
    verified. → **prompt 16**, and every prompt requires tests.

---

## How to use these

Open a session in the target repo and give it the shared context plus that repo's prompt:

```
Read /Users/princeokechukwu/source/repos/swiftbon/_prompts/00-READ-FIRST-shared-context.md
then follow /Users/princeokechukwu/source/repos/swiftbon/_prompts/03-postings-service.md
```

Each prompt has **Current state** (verified facts with file:line), **Tasks**,
**Verification**, **Acceptance criteria**, and **Out of scope**. The out-of-scope sections
are deliberate — they keep the changes reviewable.

## Three things worth deciding before starting

1. **Prompt 09 (bani)** and **prompt 11 (flights)** both open with a decision, not a task.
   Bani is barely implemented; flights is a byte-identical copy of billers with no flights
   code. Both ask whether the service should exist before any work happens. Getting those
   answers early avoids building multi-currency support into something about to be
   deleted.

2. **Compliance sign-off.** Several prompts build FINTRAC-shaped hooks — 24-hour
   aggregation, reportable-threshold events, per-currency tier limits, Canadian identity
   verification. They deliberately implement the *mechanism* and leave thresholds and
   filing decisions as clearly-marked placeholder configuration. Those need Swiftbon's
   compliance officer before go-live, and they are called out in each prompt rather than
   guessed at.

3. **The FX position is a real financial exposure.** Once conversions run, an unhedged
   balance accumulates in the `FX_POSITION_<CCY>` accounts and Swiftbon carries the market
   risk. Prompt 15 flags this as out of scope for code but needing a treasury process. It
   should not be discovered after the first large conversion.

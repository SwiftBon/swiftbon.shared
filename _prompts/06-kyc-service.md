# 06 — `swiftbon.kyc.service` — jurisdiction-routed identity verification

> Read `00-READ-FIRST-shared-context.md` first. Requires `01`, `02`.

---

## Prompt

You are working in
`/Users/princeokechukwu/source/repos/swiftbon/swiftbon.kyc.service`
(module `swiftbon.com/kyc`, Go 1.24.1).

Swiftbon is a Canadian licensed MSB. This service verifies customer identity. It can
currently only verify **Nigerian** identities, which means a Canadian customer cannot be
onboarded — and identity verification is the gate on account opening, so this blocks
multi-currency accounts for Canadian customers regardless of what the ledger supports.

### Current state (verified)

**Nigeria-only providers.** SmileID for identity (`config/config.yml:45-54`) and Axta for
address verification (`:73-75`). Outbound calls in
`internal/usecase/webapi/{smileId,facematch,axtaAddressVerification}.go`.

**Nigeria-only ID types.** `internal/enums/id_type.go`: `BVN`, `NIN_V2`, `NIN_Slip`,
`V_NIN`, `Drivers_License`, `VotersCard`, `BankAccount`, `Phonenumber`.

**Hardcoded `+234`.** `internal/util/string_manipulation.go:7,11,23`:
```go
func AddCountryCodeToPhoneNumber(phonenumber string) string { return "+234" + phonenumber }
```

**Routes.**
```
POST /kyc/v1/api/id/verify              verifyID            internal/.../kyc.go:41
POST /kyc/v1/api/id/verify/async        verifyIDAsync       kyc.go:74
POST /kyc/v1/api/user/facematch         performFacematch    kyc.go:107
POST /kyc-webhook/v1/api/webhook        processWebhook      kycWebhook.go:17  (no auth)
```
gRPC exposes only `KycMs.VerifyId`.

**Tables** (no schema prefix): `smile_jobs`, `verifications`, `events`,
`address_verification_jobs`. Note `smile_jobs` is named for one specific vendor — a
provider-shaped table, not a domain-shaped one.

**Events.** Publishes `bvn-verified`, `means-of-identification-verified`,
`id-verification-failed`, `address-verification-successful`,
`address-verification-failed`. Consumes `verify-address`. Account service consumes the
verified events to update the customer.

**Config inconsistency.** This repo reads `os.Getenv("CONFIG_FILE_LOCATION")`
(`config/config.go:141`) where every other service reads `CONFIG_LOCATION`.

**A copy-paste bug:** the webhook router's log tag says
`microservice: swiftbon.paystack.service` (`webhookRouter.go:45`).

**`dlib` is built from source** on the deployment host for face matching
(`swiftbon.go.deployment.template/scripts/run.sh`), and
`FACE_RECOGNITION_MODEL_FOLDER_LOCATION` points at local model files. Face matching runs
in-process, not via a provider.

---

## Tasks

### 1. Jurisdiction routing

Introduce a provider abstraction selected by the customer's country. This is the core
change; everything else follows from it.

```go
// internal/usecase/webapi/apis/identityProvider.go
type IdentityProvider interface {
    Name() string
    SupportedCountries() []string          // ISO 3166-1 alpha-2
    SupportedIDTypes(country string) []enums.IdType
    VerifyIdentity(ctx context.Context, req VerifyIdentityRequest) (VerificationResult, error)
    VerifyAddress(ctx context.Context, req VerifyAddressRequest) (VerificationResult, error)
    SupportsAsync() bool
}
```

A registry resolves country → provider:

```go
type ProviderRegistry interface {
    For(country string) (IdentityProvider, error)   // error, never a silent fallback
}
```

**Never fall back to a provider that does not cover the country.** Sending a Canadian
passport to a Nigerian BVN endpoint produces a confident-looking failure that reads as
"identity rejected" rather than "wrong provider" — and a false rejection at onboarding is
indistinguishable from fraud from the outside.

Wrap the existing SmileID and Axta code as the `NG` implementation. Do not rewrite it.

### 2. Canadian provider

Add a `CA` implementation. Canadian identity verification for an MSB typically needs one
of:

- **Government photo ID** — passport, provincial driver's licence, PR card — validated
  and matched to the person (document authentication plus liveness).
- **Credit-file** verification against a Canadian bureau.
- **Dual-process** — two independent reliable sources confirming name plus one of
  address or date of birth.

The vendor is a commercial and compliance decision, not an engineering one. Build the
adapter against the interface, keep the vendor behind config, and write the README
section that says which capabilities the chosen vendor must provide. Flag in the PR that
this needs compliance sign-off before go-live.

Also required for an MSB and absent today:

- **Sanctions and PEP screening** at onboarding and on an ongoing basis. Add a
  `screening_results` table and a `ScreeningProvider` interface. `customers` already has
  a `politocally_exposed` [sic] boolean but nothing populates it from a screening
  source.
- **Rescreening.** Sanctions lists change; a customer clear at onboarding may not stay
  clear. Add a cron that rescreens and emits an event on a new hit.

> Build the mechanism and the store. Do not hardcode a compliance decision about what a
> hit means — emit an event and let the compliance workflow decide.

### 3. Country-scoped ID types

Keep the Nigerian enum values — NGN accounts still need BVN and NIN. Add Canadian ones
and make validity a function of country:

```go
func ValidIDTypesForCountry(country string) []IdType
```

| Country | Accepted |
|---|---|
| NG | BVN, NIN_V2, NIN_Slip, V_NIN, Drivers_License, VotersCard, Passport |
| CA | Passport, ProvincialDriversLicence, PRCard, CitizenshipCard |

Add the new enum values at the end of the `iota` sequence. **Do not renumber existing
values** — they are persisted as integers in `verifications` and in the account service's
`customers.means_of_identification`.

Add `country` to `verifications` and `address_verification_jobs`, backfilled to `NG`.

If a Canadian SIN is ever accepted, store a salted hash plus last 3 digits only. Never
plaintext, never in logs, never in an event payload. Prefer not collecting it at all —
see prompt 02.

### 4. Phone numbers

Replace `AddCountryCodeToPhoneNumber` with E.164 normalisation, matching prompt 02
exactly so the two services agree on the canonical form:

```go
func NormalizePhoneNumber(raw, countryISO2 string) (string, error)
```

Use `github.com/nyaruka/phonenumbers`. This matters because kyc looks customers up by
phone number via the account service — if the two normalise differently, lookups miss and
verification silently fails to attach to a customer.

### 5. Domain-shaped tables

`smile_jobs` is named for one vendor. Rename to `provider_jobs` with a `provider`
column, and migrate existing rows with `provider = 'smileid'`. Otherwise every new
provider adds a table.

Keep the existing table as a view named `smile_jobs` for one release if anything queries
it directly; check first.

### 6. Currency and jurisdiction feed account opening

Prompt 02 makes account opening reject currencies the customer's KYC state does not
permit. This service supplies that state.

Add `verified_jurisdictions` to the customer's verification record — the set of countries
for which identity has been verified to a sufficient standard. Publish it on the
`*-verified` events with `schema_version: 2` so the account service can gate currency
selection on it.

Whether holding a given currency requires a given jurisdiction's verification is a
compliance rule, not a technical one. Implement it as configuration
(`currency → required verification level`) with placeholder values clearly marked, and
say so in the PR.

### 7. Defects to fix in passing

- The webhook endpoint has **no auth middleware** and no signature verification. SmileID
  supports callback verification — use it. An unauthenticated KYC webhook can mark a
  customer verified.
- `md.Get("x-client-id")[0]` panics on metadata without that key.
- `CONFIG_FILE_LOCATION` → `CONFIG_LOCATION` to match the rest of the estate. Support
  both for one release.
- `webhookRouter.go:45` log tag says `swiftbon.paystack.service`.
- `config/config.yml` contains the SmileID signature and Axta API key in plaintext.
  Raise separately; rotate.
- The README is stale boilerplate titled "Persian Black AccountMs Service" describing an
  SMS/email service. Replace it.
- CI pins Go 1.17 against a `go 1.24.1` module.

---

## Verification

```bash
cd /Users/princeokechukwu/source/repos/swiftbon/swiftbon.kyc.service
make compose-up && make migrate-up && make test   # -race
```

- Verify a Nigerian customer with a BVN: routes to SmileID, succeeds exactly as before.
  **This is the regression test — existing NG onboarding must not change behaviour.**
- Verify a Canadian customer with a passport: routes to the CA provider.
- Submit a Canadian customer with a BVN ID type: rejected as invalid for country,
  **not** sent to SmileID.
- Submit a customer with an unsupported country: explicit error, no provider called.
- Post an unsigned webhook: rejected.
- Confirm `*-verified` events carry `country` and `verified_jurisdictions`, and that the
  account service updates the customer from them.
- Phone normalisation round-trips identically to the account service's, proven by a test
  using the same input table in both repos.

## Acceptance criteria

- [ ] Provider selected by country through a registry; no fallback across jurisdictions.
- [ ] Nigerian verification is byte-for-byte unchanged in behaviour.
- [ ] A Canadian customer can be verified with passport or provincial DL.
- [ ] ID types validated against country; existing enum integers unchanged.
- [ ] Sanctions/PEP screening runs at onboarding and on a rescreening schedule, with
      results stored and events emitted.
- [ ] Phone normalisation is E.164 and identical to the account service's.
- [ ] `provider_jobs` replaces `smile_jobs`, existing rows migrated.
- [ ] Verification events carry country and verified jurisdictions.
- [ ] Webhook is authenticated and signature-verified.
- [ ] Compliance-dependent thresholds and rules are configuration, marked as
      placeholders, and called out in the PR.

## Out of scope

- Choosing the Canadian vendor (commercial decision).
- The face-matching `dlib` pipeline — leave it as is.
- Rotating committed credentials (raise separately).

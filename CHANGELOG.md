# Changelog

## v0.1.0 (web dashboard)

Everything below is additive. Nothing in `v0.0.1` was removed, renamed or
renumbered; `proto/wirecompat_test.go` proves the `v0.0.1` wire bytes still
decode and re-encode identically.

### Added

- `principal/`: the internal principal token. Ed25519 `Signer` (gateway only) and
  `KeyVerifier` with a rotatable key set; Echo middleware `Require` with
  `WithPerm`, `WithAuthLevel`, `DenyReadOnly`, `WithRealm`; a gRPC unary
  interceptor with the same semantics; the permission names; the shared
  `UNAUTHENTICATED` and `FORBIDDEN` codes.
- `events/`: a payload struct, topic constant and schema version for each of the
  53 dashboard topics, the `Envelope` (seahorse's JSON field names plus
  `event_id`), `Decode`/`DecodeAs`, and a golden JSON file per topic.
- `refs/`: `Format` and strict `Parse` for `PO`, `BEN`, `SUP`, `DEC`, `CASE`,
  `RFI`, `APR`.
- `names/`: `Normalise` and `Match`, a token-set matcher with company-suffix
  equivalence. The threshold is a parameter.
- `settings/`: typed registry, versioned immutable store (memory and Postgres),
  60-second cached `Get` with org-override precedence, `Propose`/`Apply`, boot
  `Seed`, Echo handlers, and the migration template.
- `money.Equivalent` (mid-rate, indicative only) and `money.ParseMinor`.
- `proto/rails/v1`, `identity/v1`, `approvals/v1`, `kyc/v1`, `documents/v1`,
  `compliance/v1`, `treasury/v1`.
- `postings.v1`: `PlaceHold`, `ReleaseHold`, `CaptureHold`, `GetHold`,
  `GetAvailableBalances`, `GetStatement`, `ListEntries`,
  `SumCustomerLiabilities`, and `hold` (field 13) on `PostingRequest`.
- `account.v1`: `CreateBusiness`, `GetBusiness`, `OpenBusinessAccount`,
  `ListOrgAccounts`, `GetCapabilities`, `SetCapability`,
  `IssueReceivingDetailsVersion`.
- `fx.v1`: `Equivalent`.

### Changed

- The README scope statement now includes `principal/`, `events/`, `refs/`,
  `names/` and `settings/`, with the reason.
- `make test-cover` covers the new packages. Generated Go was regenerated with
  protoc 36.1, so the `protoc` line in the generated headers changed.

### Notes for consumers

- `go.mod` gains `github.com/google/uuid`, `github.com/labstack/echo/v4`,
  `golang.org/x/text` and, for the Postgres store's tests only,
  `github.com/jackc/pgx/v5`. Importing `principal` or `settings` brings in Echo;
  the money, fx, account and proto packages do not.

## v0.0.1

Money and currency model, FX quote types, account types, and the `money.v1`,
`fx.v1`, `account.v1`, `postings.v1` and `seahorse.v1` protos.

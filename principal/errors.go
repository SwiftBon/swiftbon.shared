package principal

import "errors"

// Response codes every service returns for a failed principal check, so a
// client sees the same string whichever service refused it.
const (
	CodeUnauthenticated = "UNAUTHENTICATED"
	CodeForbidden       = "FORBIDDEN"
)

// Reasons a request is forbidden. They travel in the response description so
// the gateway can map them to the public error codes (for example
// mfa_device_required) without parsing prose.
const (
	ReasonPermission   = "permission_required"
	ReasonAuthLevel    = "mfa_device_required"
	ReasonReadOnly     = "read_only_principal"
	ReasonRealm        = "wrong_realm"
	ReasonUnauthorised = "not_authenticated"
)

// ErrUnauthenticated is matched (errors.Is) by every verification failure.
var ErrUnauthenticated = errors.New("principal: unauthenticated")

// ErrForbidden is matched (errors.Is) by every authorisation failure.
var ErrForbidden = errors.New("principal: forbidden")

// Verification failures. Each also matches ErrUnauthenticated.
var (
	ErrMissing     = newAuthnError("principal token missing")
	ErrMalformed   = newAuthnError("principal token malformed")
	ErrAlgorithm   = newAuthnError("principal token algorithm not allowed")
	ErrUnknownKey  = newAuthnError("principal token signed with an unknown key id")
	ErrSignature   = newAuthnError("principal token signature invalid")
	ErrExpired     = newAuthnError("principal token expired")
	ErrNotYetValid = newAuthnError("principal token not yet valid")
	ErrLifetime    = newAuthnError("principal token lifetime exceeds the maximum")
	ErrAudience    = newAuthnError("principal token audience mismatch")
	ErrIssuer      = newAuthnError("principal token issuer mismatch")
	ErrClaims      = newAuthnError("principal token claims invalid")
)

type authnError struct{ msg string }

func newAuthnError(msg string) *authnError { return &authnError{msg: msg} }

func (e *authnError) Error() string        { return e.msg }
func (e *authnError) Is(target error) bool { return target == ErrUnauthenticated }

// Authorisation failures. Each also matches ErrForbidden.
var (
	ErrPermission = newAuthzError(ReasonPermission)
	ErrAuthLevel  = newAuthzError(ReasonAuthLevel)
	ErrReadOnly   = newAuthzError(ReasonReadOnly)
	ErrRealm      = newAuthzError(ReasonRealm)
)

type authzError struct{ reason string }

func newAuthzError(reason string) *authzError { return &authzError{reason: reason} }

func (e *authzError) Error() string        { return "principal: forbidden: " + e.reason }
func (e *authzError) Is(target error) bool { return target == ErrForbidden }

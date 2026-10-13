// Package principal is the internal identity token the gateway mints for every
// call it makes on a user's behalf, and the verifier every other service uses
// to read it.
//
// It lives in this module, not in each service, because a verifier that differs
// between services is an authorisation bypass: one service that accepts
// alg=none, or skips the audience check, undoes the gateway's work for every
// request that can reach it. Services take the organisation and the caller's
// permissions from the verified token and from nowhere else.
package principal

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// HeaderName is the HTTP header carrying the token from the gateway.
const HeaderName = "X-Swiftbon-Principal"

// MetadataKey is the gRPC metadata key carrying the token. gRPC lower-cases
// metadata keys, so this is the lower-cased form of HeaderName.
const MetadataKey = "x-swiftbon-principal"

// Issuer is the only issuer a verifier accepts.
const Issuer = "swiftbon.gateway"

// AuthLevel is how strongly the session has proven who the user is. The values
// are ordered: a higher level satisfies a requirement for any lower one.
type AuthLevel int

const (
	// authLevelUnknown is the zero value. It satisfies no requirement and is
	// never produced by a valid token.
	authLevelUnknown AuthLevel = iota
	// FirstFactor is an email code only: the user can view, and act on nothing.
	FirstFactor
	// MFA is the email code plus TOTP.
	MFA
	// MFADevice is MFA plus a bound WebAuthn device. It is required to move
	// money, approve, change beneficiaries or manage the team.
	MFADevice
)

var authLevelNames = map[AuthLevel]string{
	FirstFactor: "first_factor",
	MFA:         "mfa",
	MFADevice:   "mfa_device",
}

// String returns the wire name. It never panics on an unexpected value.
func (l AuthLevel) String() string {
	if n, ok := authLevelNames[l]; ok {
		return n
	}
	return fmt.Sprintf("AuthLevel(%d)", int(l))
}

// ParseAuthLevel is the inverse of String. An unknown name is an error, never a
// default: defaulting to the lowest level would be safe, but defaulting to any
// other would silently grant access.
func ParseAuthLevel(s string) (AuthLevel, error) {
	for l, n := range authLevelNames {
		if n == s {
			return l, nil
		}
	}
	return authLevelUnknown, fmt.Errorf("principal: unknown auth level %q", s)
}

// Realm separates customers from Swiftbon staff. A staff principal is refused
// on customer routes and a customer principal on ops routes.
type Realm string

const (
	Customer Realm = "customer"
	Staff    Realm = "staff"
)

func (r Realm) valid() bool { return r == Customer || r == Staff }

// Principal is the verified caller.
type Principal struct {
	UserID uuid.UUID
	// OrgID is the tenant. Every query a service runs for this caller is
	// filtered by it. A staff principal that is not impersonating has none.
	OrgID     uuid.UUID
	Role      string
	Perms     []string
	SessionID string
	// AMR lists the authentication methods used: email_code, totp, webauthn,
	// api_key.
	AMR       []string
	AuthLevel AuthLevel
	// ImpersonatorID is the staff member acting as this user, when set.
	ImpersonatorID *uuid.UUID
	// ReadOnly principals may read and must be refused anything that changes
	// state. Impersonation always sets it.
	ReadOnly bool
	Realm    Realm
}

// HasPerm reports whether the principal holds the permission.
func (p Principal) HasPerm(perm string) bool {
	for _, have := range p.Perms {
		if have == perm {
			return true
		}
	}
	return false
}

// Impersonated reports whether a staff member is acting as this user.
func (p Principal) Impersonated() bool { return p.ImpersonatorID != nil }

type ctxKey struct{}

// WithContext returns ctx carrying p.
func WithContext(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the principal the middleware or interceptor put on ctx.
func From(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

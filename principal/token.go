package principal

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	// algEdDSA is the only signature algorithm. A verifier that honours the
	// header's alg is a verifier an attacker picks the algorithm for.
	algEdDSA = "EdDSA"

	// MaxLifetime is the longest a token may live (exp - iat). It is enforced
	// on verify, not just on sign, so a leaked signing key cannot mint
	// long-lived tokens that every service would accept.
	MaxLifetime = 60 * time.Second

	// DefaultSkew is the clock skew tolerated between the gateway and a service.
	DefaultSkew = 5 * time.Second
)

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

// claims is the wire form of a Principal plus the registered JWT claims.
type claims struct {
	Iss       string          `json:"iss"`
	Sub       string          `json:"sub"`
	Aud       json.RawMessage `json:"aud"`
	Exp       int64           `json:"exp"`
	Iat       int64           `json:"iat"`
	JTI       string          `json:"jti,omitempty"`
	Org       string          `json:"org,omitempty"`
	Role      string          `json:"role,omitempty"`
	Perms     []string        `json:"perms,omitempty"`
	SID       string          `json:"sid,omitempty"`
	AMR       []string        `json:"amr,omitempty"`
	AuthLevel string          `json:"auth_level"`
	Imp       string          `json:"imp,omitempty"`
	RO        bool            `json:"ro,omitempty"`
	Realm     string          `json:"realm"`
}

var b64 = base64.RawURLEncoding

// Signer mints principal tokens. Only the gateway holds one.
type Signer struct {
	kid      string
	key      ed25519.PrivateKey
	lifetime time.Duration
	now      func() time.Time
}

// SignerOption configures a Signer.
type SignerOption func(*Signer)

// WithSignerClock replaces time.Now, for tests.
func WithSignerClock(now func() time.Time) SignerOption {
	return func(s *Signer) { s.now = now }
}

// WithLifetime sets the token lifetime. It must be in (0, MaxLifetime].
func WithLifetime(d time.Duration) SignerOption {
	return func(s *Signer) { s.lifetime = d }
}

// NewSigner returns a Signer that tags tokens with kid so verifiers can find
// the matching public key during a rotation.
func NewSigner(kid string, key ed25519.PrivateKey, opts ...SignerOption) (*Signer, error) {
	if kid == "" {
		return nil, errors.New("principal: signer needs a key id")
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("principal: signer needs an Ed25519 private key")
	}
	s := &Signer{kid: kid, key: key, lifetime: MaxLifetime, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	if s.lifetime <= 0 || s.lifetime > MaxLifetime {
		return nil, fmt.Errorf("principal: lifetime must be in (0, %s]", MaxLifetime)
	}
	return s, nil
}

// Sign mints a token for p, valid for the named services. Naming several
// audiences lets one token cover a request the gateway fans out; each service
// still accepts it only if its own name is listed.
func (s *Signer) Sign(p Principal, audience ...string) (string, error) {
	if len(audience) == 0 {
		return "", errors.New("principal: token needs at least one audience")
	}
	for _, a := range audience {
		if a == "" {
			return "", errors.New("principal: empty audience")
		}
	}
	if err := validatePrincipal(p); err != nil {
		return "", err
	}
	aud, err := json.Marshal(audience)
	if err != nil {
		return "", err
	}
	now := s.now()
	c := claims{
		Iss:       Issuer,
		Sub:       p.UserID.String(),
		Aud:       aud,
		Iat:       now.Unix(),
		Exp:       now.Add(s.lifetime).Unix(),
		JTI:       uuid.NewString(),
		Role:      p.Role,
		Perms:     p.Perms,
		SID:       p.SessionID,
		AMR:       p.AMR,
		AuthLevel: p.AuthLevel.String(),
		RO:        p.ReadOnly,
		Realm:     string(p.Realm),
	}
	if p.OrgID != uuid.Nil {
		c.Org = p.OrgID.String()
	}
	if p.ImpersonatorID != nil {
		c.Imp = p.ImpersonatorID.String()
	}
	hb, err := json.Marshal(header{Alg: algEdDSA, Typ: "JWT", Kid: s.kid})
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	input := b64.EncodeToString(hb) + "." + b64.EncodeToString(cb)
	return input + "." + b64.EncodeToString(ed25519.Sign(s.key, []byte(input))), nil
}

// validatePrincipal rejects a principal no verifier would accept, at the point
// the mistake is made rather than as a 401 two services later.
func validatePrincipal(p Principal) error {
	if p.UserID == uuid.Nil {
		return errors.New("principal: user id required")
	}
	if !p.Realm.valid() {
		return fmt.Errorf("principal: unknown realm %q", p.Realm)
	}
	if p.Realm == Customer && p.OrgID == uuid.Nil {
		return errors.New("principal: customer principal needs an organisation")
	}
	if _, ok := authLevelNames[p.AuthLevel]; !ok {
		return fmt.Errorf("principal: invalid auth level %d", int(p.AuthLevel))
	}
	if p.ImpersonatorID != nil && !p.ReadOnly {
		return errors.New("principal: an impersonating principal must be read-only")
	}
	return nil
}

// KeySet maps key ids to public keys.
type KeySet map[string]ed25519.PublicKey

// Verifier validates a principal token.
type Verifier interface {
	Verify(token string) (Principal, error)
}

// KeyVerifier is the Verifier every service uses. Its key set can be replaced
// at runtime (SetKeys), so the gateway's key rotates without a coordinated
// deploy: publish the new public key alongside the old, switch the signer,
// then drop the old key.
type KeyVerifier struct {
	service string
	keys    atomic.Pointer[KeySet]
	skew    time.Duration
	now     func() time.Time
}

// VerifierOption configures a KeyVerifier.
type VerifierOption func(*KeyVerifier)

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) VerifierOption {
	return func(v *KeyVerifier) { v.now = now }
}

// WithSkew sets the tolerated clock skew. The default is DefaultSkew.
func WithSkew(d time.Duration) VerifierOption {
	return func(v *KeyVerifier) { v.skew = d }
}

// NewVerifier returns a verifier for the named service. A token is accepted only
// if its audience includes service.
func NewVerifier(service string, keys KeySet, opts ...VerifierOption) (*KeyVerifier, error) {
	if service == "" {
		return nil, errors.New("principal: verifier needs the service name it is audience for")
	}
	v := &KeyVerifier{service: service, skew: DefaultSkew, now: time.Now}
	for _, o := range opts {
		o(v)
	}
	v.SetKeys(keys)
	return v, nil
}

// SetKeys atomically replaces the accepted public keys.
func (v *KeyVerifier) SetKeys(keys KeySet) {
	cp := make(KeySet, len(keys))
	for k, pub := range keys {
		cp[k] = pub
	}
	v.keys.Store(&cp)
}

// Verify checks signature, algorithm, issuer, audience and time bounds, then
// returns the principal. Every failure matches ErrUnauthenticated.
func (v *KeyVerifier) Verify(token string) (Principal, error) {
	if token == "" {
		return Principal{}, ErrMissing
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Principal{}, ErrMalformed
	}
	var h header
	if err := decodeSegment(parts[0], &h); err != nil {
		return Principal{}, ErrMalformed
	}
	// Reject on the header before looking at keys or the signature, so "none"
	// and HS256 never reach a code path that could be confused into using them.
	if h.Alg != algEdDSA {
		return Principal{}, ErrAlgorithm
	}
	pub, ok := (*v.keys.Load())[h.Kid]
	if !ok || h.Kid == "" || len(pub) != ed25519.PublicKeySize {
		return Principal{}, ErrUnknownKey
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return Principal{}, ErrMalformed
	}
	if !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		return Principal{}, ErrSignature
	}
	var c claims
	if err := decodeSegment(parts[1], &c); err != nil {
		return Principal{}, ErrMalformed
	}
	return v.principalFrom(c)
}

func (v *KeyVerifier) principalFrom(c claims) (Principal, error) {
	if c.Iss != Issuer {
		return Principal{}, ErrIssuer
	}
	if !audienceIncludes(c.Aud, v.service) {
		return Principal{}, ErrAudience
	}
	if c.Exp == 0 || c.Iat == 0 {
		return Principal{}, ErrClaims
	}
	now := v.now()
	exp, iat := time.Unix(c.Exp, 0), time.Unix(c.Iat, 0)
	if now.After(exp.Add(v.skew)) {
		return Principal{}, ErrExpired
	}
	if iat.After(now.Add(v.skew)) {
		return Principal{}, ErrNotYetValid
	}
	if exp.Sub(iat) > MaxLifetime {
		return Principal{}, ErrLifetime
	}

	user, err := uuid.Parse(c.Sub)
	if err != nil {
		return Principal{}, ErrClaims
	}
	level, err := ParseAuthLevel(c.AuthLevel)
	if err != nil {
		return Principal{}, ErrClaims
	}
	p := Principal{
		UserID:    user,
		Role:      c.Role,
		Perms:     c.Perms,
		SessionID: c.SID,
		AMR:       c.AMR,
		AuthLevel: level,
		ReadOnly:  c.RO,
		Realm:     Realm(c.Realm),
	}
	if c.Org != "" {
		if p.OrgID, err = uuid.Parse(c.Org); err != nil {
			return Principal{}, ErrClaims
		}
	}
	if c.Imp != "" {
		imp, err := uuid.Parse(c.Imp)
		if err != nil {
			return Principal{}, ErrClaims
		}
		p.ImpersonatorID = &imp
	}
	if validatePrincipal(p) != nil {
		return Principal{}, ErrClaims
	}
	return p, nil
}

// decodeSegment decodes one base64url JSON segment, rejecting trailing data so
// a token cannot smuggle a second JSON document past the parser.
func decodeSegment(seg string, into any) error {
	raw, err := b64.DecodeString(seg)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(into); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}

// audienceIncludes accepts aud as a string or an array of strings, per RFC 7519.
func audienceIncludes(raw json.RawMessage, service string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == service
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, a := range many {
			if a == service {
				return true
			}
		}
	}
	return false
}

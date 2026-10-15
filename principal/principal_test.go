package principal_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"swiftbon.com/shared/principal"
)

var t0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

type fixture struct {
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
	signer *principal.Signer
	ver    *principal.KeyVerifier
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{pub: pub, priv: priv, now: t0}
	f.signer, err = principal.NewSigner("k1", priv, principal.WithSignerClock(func() time.Time { return f.now }))
	if err != nil {
		t.Fatal(err)
	}
	f.ver, err = principal.NewVerifier("payouts", principal.KeySet{"k1": pub}, principal.WithClock(func() time.Time { return f.now }))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func customer() principal.Principal {
	return principal.Principal{
		UserID:    uuid.New(),
		OrgID:     uuid.New(),
		Role:      "finance_maker",
		Perms:     []string{principal.BalancesView, principal.PayoutsInitiate},
		SessionID: "sess-1",
		AMR:       []string{"email_code", "totp", "webauthn"},
		AuthLevel: principal.MFADevice,
		Realm:     principal.Customer,
	}
}

func (f *fixture) sign(t *testing.T, p principal.Principal, aud ...string) string {
	t.Helper()
	if len(aud) == 0 {
		aud = []string{"payouts"}
	}
	tok, err := f.signer.Sign(p, aud...)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func seg(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// forge signs arbitrary header and claims with priv, so a test can build the
// tokens a correct Signer would refuse to.
func forge(t *testing.T, priv ed25519.PrivateKey, h, c map[string]any) string {
	t.Helper()
	in := seg(t, h) + "." + seg(t, c)
	return in + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(in)))
}

func goodClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss": principal.Issuer, "sub": uuid.NewString(), "aud": "payouts",
		"iat": now.Unix(), "exp": now.Add(30 * time.Second).Unix(),
		"org": uuid.NewString(), "auth_level": "mfa_device", "realm": "customer",
	}
}

func goodHeader() map[string]any {
	return map[string]any{"alg": "EdDSA", "typ": "JWT", "kid": "k1"}
}

func TestRoundTrip(t *testing.T) {
	f := newFixture(t)
	want := customer()
	imp := uuid.New()
	want.ImpersonatorID, want.ReadOnly = &imp, true

	got, err := f.ver.Verify(f.sign(t, want))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.UserID != want.UserID || got.OrgID != want.OrgID || got.Role != want.Role ||
		got.SessionID != want.SessionID || got.AuthLevel != want.AuthLevel ||
		got.Realm != want.Realm || !got.ReadOnly || !got.Impersonated() || *got.ImpersonatorID != imp {
		t.Errorf("round trip lost data: got %+v want %+v", got, want)
	}
	if !got.HasPerm(principal.PayoutsInitiate) || got.HasPerm(principal.ApprovalsDecide) {
		t.Errorf("perms wrong: %v", got.Perms)
	}
	if strings.Join(got.AMR, ",") != "email_code,totp,webauthn" {
		t.Errorf("amr = %v", got.AMR)
	}
}

func TestStaffPrincipalWithoutOrg(t *testing.T) {
	f := newFixture(t)
	p := principal.Principal{UserID: uuid.New(), AuthLevel: principal.MFADevice, Realm: principal.Staff}
	got, err := f.ver.Verify(f.sign(t, p))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.OrgID != uuid.Nil || got.Realm != principal.Staff {
		t.Errorf("got %+v", got)
	}
}

func TestVerifyFailures(t *testing.T) {
	f := newFixture(t)
	otherPub, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	_ = otherPub

	valid := f.sign(t, customer())
	parts := strings.Split(valid, ".")

	// A payload re-encoded with a different org, keeping the old signature.
	var tampered map[string]any
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(raw, &tampered)
	tampered["org"] = uuid.NewString()
	tamperedTok := parts[0] + "." + seg(t, tampered) + "." + parts[2]

	with := func(mut func(c map[string]any)) string {
		c := goodClaims(f.now)
		mut(c)
		return forge(t, f.priv, goodHeader(), c)
	}

	tests := []struct {
		name  string
		token string
		want  error
	}{
		{"missing", "", principal.ErrMissing},
		{"not three parts", "a.b", principal.ErrMalformed},
		{"header not base64", "!!!." + parts[1] + "." + parts[2], principal.ErrMalformed},
		{"header not json", "e30x." + parts[1] + "." + parts[2], principal.ErrMalformed},
		{"signature not base64", parts[0] + "." + parts[1] + ".!!!", principal.ErrMalformed},
		{"alg none", forge(t, f.priv, map[string]any{"alg": "none", "kid": "k1"}, goodClaims(f.now)), principal.ErrAlgorithm},
		{"alg none empty signature", seg(t, map[string]any{"alg": "none", "kid": "k1"}) + "." + seg(t, goodClaims(f.now)) + ".", principal.ErrAlgorithm},
		{"alg HS256", forge(t, f.priv, map[string]any{"alg": "HS256", "kid": "k1"}, goodClaims(f.now)), principal.ErrAlgorithm},
		{"alg lower-case eddsa", forge(t, f.priv, map[string]any{"alg": "eddsa", "kid": "k1"}, goodClaims(f.now)), principal.ErrAlgorithm},
		{"unknown kid", forge(t, f.priv, map[string]any{"alg": "EdDSA", "kid": "k9"}, goodClaims(f.now)), principal.ErrUnknownKey},
		{"missing kid", forge(t, f.priv, map[string]any{"alg": "EdDSA"}, goodClaims(f.now)), principal.ErrUnknownKey},
		{"signed by another key", forge(t, otherPriv, goodHeader(), goodClaims(f.now)), principal.ErrSignature},
		{"tampered payload", tamperedTok, principal.ErrSignature},
		{"claims not json", func() string {
			in := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte("nope"))
			return in + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.priv, []byte(in)))
		}(), principal.ErrMalformed},
		{"claims with trailing document", func() string {
			in := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{} {}`))
			return in + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.priv, []byte(in)))
		}(), principal.ErrMalformed},
		{"wrong audience", f.sign(t, customer(), "kyc"), principal.ErrAudience},
		{"audience wrong type", with(func(c map[string]any) { c["aud"] = 7 }), principal.ErrAudience},
		{"wrong issuer", with(func(c map[string]any) { c["iss"] = "evil" }), principal.ErrIssuer},
		{"expired beyond skew", with(func(c map[string]any) {
			c["iat"], c["exp"] = f.now.Add(-time.Hour).Unix(), f.now.Add(-time.Hour+30*time.Second).Unix()
		}), principal.ErrExpired},
		{"issued in the future", with(func(c map[string]any) {
			c["iat"], c["exp"] = f.now.Add(time.Minute).Unix(), f.now.Add(time.Minute+30*time.Second).Unix()
		}), principal.ErrNotYetValid},
		{"lifetime too long", with(func(c map[string]any) { c["exp"] = f.now.Add(10 * time.Minute).Unix() }), principal.ErrLifetime},
		{"no exp", with(func(c map[string]any) { delete(c, "exp") }), principal.ErrClaims},
		{"no iat", with(func(c map[string]any) { delete(c, "iat") }), principal.ErrClaims},
		{"bad sub", with(func(c map[string]any) { c["sub"] = "x" }), principal.ErrClaims},
		{"nil sub", with(func(c map[string]any) { c["sub"] = uuid.Nil.String() }), principal.ErrClaims},
		{"bad org", with(func(c map[string]any) { c["org"] = "x" }), principal.ErrClaims},
		{"customer without org", with(func(c map[string]any) { delete(c, "org") }), principal.ErrClaims},
		{"bad auth level", with(func(c map[string]any) { c["auth_level"] = "root" }), principal.ErrClaims},
		{"bad realm", with(func(c map[string]any) { c["realm"] = "alien" }), principal.ErrClaims},
		{"bad imp", with(func(c map[string]any) { c["imp"] = "x" }), principal.ErrClaims},
		{"impersonation not read-only", with(func(c map[string]any) { c["imp"] = uuid.NewString() }), principal.ErrClaims},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.ver.Verify(tc.token)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !errors.Is(err, principal.ErrUnauthenticated) {
				t.Errorf("%v does not match ErrUnauthenticated", err)
			}
		})
	}
}

func TestExpiryAndSkew(t *testing.T) {
	f := newFixture(t)
	tok := f.sign(t, customer()) // valid 60s

	f.now = t0.Add(60*time.Second + 4*time.Second)
	if _, err := f.ver.Verify(tok); err != nil {
		t.Errorf("within skew should verify: %v", err)
	}
	f.now = t0.Add(60*time.Second + 6*time.Second)
	if _, err := f.ver.Verify(tok); !errors.Is(err, principal.ErrExpired) {
		t.Errorf("past skew: err = %v", err)
	}

	f.now = t0
	tok2 := f.sign(t, customer())
	f.now = t0.Add(-4 * time.Second) // verifier's clock runs behind the gateway
	if _, err := f.ver.Verify(tok2); err != nil {
		t.Errorf("small negative skew should verify: %v", err)
	}
}

func TestCustomSkew(t *testing.T) {
	f := newFixture(t)
	v, err := principal.NewVerifier("payouts", principal.KeySet{"k1": f.pub},
		principal.WithClock(func() time.Time { return f.now }), principal.WithSkew(0))
	if err != nil {
		t.Fatal(err)
	}
	tok := f.sign(t, customer())
	f.now = t0.Add(61 * time.Second)
	if _, err := v.Verify(tok); !errors.Is(err, principal.ErrExpired) {
		t.Errorf("err = %v", err)
	}
}

func TestKeyRotation(t *testing.T) {
	f := newFixture(t)
	newPub, newPriv, _ := ed25519.GenerateKey(rand.Reader)
	newSigner, err := principal.NewSigner("k2", newPriv, principal.WithSignerClock(func() time.Time { return f.now }))
	if err != nil {
		t.Fatal(err)
	}
	tokNew, _ := newSigner.Sign(customer(), "payouts")
	if _, err := f.ver.Verify(tokNew); !errors.Is(err, principal.ErrUnknownKey) {
		t.Fatalf("before rotation: err = %v", err)
	}

	// Both keys live: tokens from either signer verify.
	f.ver.SetKeys(principal.KeySet{"k1": f.pub, "k2": newPub})
	if _, err := f.ver.Verify(tokNew); err != nil {
		t.Errorf("new key: %v", err)
	}
	if _, err := f.ver.Verify(f.sign(t, customer())); err != nil {
		t.Errorf("old key during overlap: %v", err)
	}

	// Old key retired.
	f.ver.SetKeys(principal.KeySet{"k2": newPub})
	if _, err := f.ver.Verify(f.sign(t, customer())); !errors.Is(err, principal.ErrUnknownKey) {
		t.Errorf("retired key: err = %v", err)
	}
}

func TestMultiAudience(t *testing.T) {
	f := newFixture(t)
	if _, err := f.ver.Verify(f.sign(t, customer(), "postings", "payouts")); err != nil {
		t.Errorf("listed audience should verify: %v", err)
	}
}

func TestSignerValidation(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := principal.NewSigner("", priv); err == nil {
		t.Error("empty kid accepted")
	}
	if _, err := principal.NewSigner("k", ed25519.PrivateKey("short")); err == nil {
		t.Error("short key accepted")
	}
	for _, d := range []time.Duration{0, -time.Second, 61 * time.Second} {
		if _, err := principal.NewSigner("k", priv, principal.WithLifetime(d)); err == nil {
			t.Errorf("lifetime %s accepted", d)
		}
	}
	if _, err := principal.NewSigner("k", priv, principal.WithLifetime(10*time.Second)); err != nil {
		t.Errorf("valid lifetime refused: %v", err)
	}

	s, _ := principal.NewSigner("k", priv)
	imp := uuid.New()
	bad := map[string]principal.Principal{
		"no user":      {Realm: principal.Customer, OrgID: uuid.New(), AuthLevel: principal.MFA},
		"bad realm":    {UserID: uuid.New(), Realm: "x", AuthLevel: principal.MFA},
		"no org":       {UserID: uuid.New(), Realm: principal.Customer, AuthLevel: principal.MFA},
		"bad level":    {UserID: uuid.New(), Realm: principal.Staff},
		"writable imp": {UserID: uuid.New(), Realm: principal.Staff, AuthLevel: principal.MFA, ImpersonatorID: &imp},
	}
	for name, p := range bad {
		if _, err := s.Sign(p, "payouts"); err == nil {
			t.Errorf("%s: Sign accepted an invalid principal", name)
		}
	}
	if _, err := s.Sign(customer()); err == nil {
		t.Error("no audience accepted")
	}
	if _, err := s.Sign(customer(), ""); err == nil {
		t.Error("empty audience accepted")
	}
}

func TestNewVerifierNeedsService(t *testing.T) {
	if _, err := principal.NewVerifier("", nil); err == nil {
		t.Error("empty service accepted")
	}
}

func TestAuthLevel(t *testing.T) {
	if !(principal.FirstFactor < principal.MFA && principal.MFA < principal.MFADevice) {
		t.Fatal("auth levels are not ordered")
	}
	for _, l := range []principal.AuthLevel{principal.FirstFactor, principal.MFA, principal.MFADevice} {
		got, err := principal.ParseAuthLevel(l.String())
		if err != nil || got != l {
			t.Errorf("ParseAuthLevel(%q) = %v, %v", l.String(), got, err)
		}
	}
	if _, err := principal.ParseAuthLevel("root"); err == nil {
		t.Error("unknown level parsed")
	}
	if s := principal.AuthLevel(0).String(); s != "AuthLevel(0)" {
		t.Errorf("zero String() = %q", s)
	}
}

func TestAllPermsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range principal.AllPerms() {
		if seen[p] {
			t.Errorf("duplicate perm %q", p)
		}
		seen[p] = true
	}
	if len(seen) != 11 {
		t.Errorf("expected 11 permissions, got %d", len(seen))
	}
}

func TestFromWithoutPrincipal(t *testing.T) {
	if _, ok := principal.From(context.Background()); ok {
		t.Error("From on empty context returned a principal")
	}
}

// ---- Echo middleware --------------------------------------------------

func serve(t *testing.T, f *fixture, token string, opts ...principal.Option) (*httptest.ResponseRecorder, *principal.Principal) {
	t.Helper()
	e := echo.New()
	var seen *principal.Principal
	e.GET("/x", func(c echo.Context) error {
		p, ok := principal.From(c.Request().Context())
		if ok {
			seen = &p
		}
		return c.String(http.StatusOK, "ok")
	}, principal.Require(f.ver, opts...))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if token != "" {
		req.Header.Set(principal.HeaderName, token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec, seen
}

func TestMiddleware(t *testing.T) {
	f := newFixture(t)

	maker := customer()
	checkerOnlyMFA := customer()
	checkerOnlyMFA.AuthLevel = principal.MFA
	ro := customer()
	ro.ReadOnly = true
	staff := principal.Principal{UserID: uuid.New(), AuthLevel: principal.MFADevice, Realm: principal.Staff}

	tests := []struct {
		name     string
		p        *principal.Principal
		token    string
		opts     []principal.Option
		status   int
		code     string
		reason   string
		wantSeen bool
	}{
		{name: "valid, no requirements", p: &maker, status: 200, wantSeen: true},
		{name: "missing header", status: 401, code: principal.CodeUnauthenticated, reason: principal.ReasonUnauthorised},
		{name: "garbage token", token: "garbage", status: 401, code: principal.CodeUnauthenticated, reason: principal.ReasonUnauthorised},
		{name: "has required perm", p: &maker, opts: []principal.Option{principal.WithPerm(principal.PayoutsInitiate)}, status: 200, wantSeen: true},
		{name: "lacks perm", p: &maker, opts: []principal.Option{principal.WithPerm(principal.ApprovalsDecide)}, status: 403, code: principal.CodeForbidden, reason: principal.ReasonPermission},
		{name: "second perm missing", p: &maker, opts: []principal.Option{principal.WithPerm(principal.PayoutsInitiate), principal.WithPerm(principal.TeamManage)}, status: 403, code: principal.CodeForbidden, reason: principal.ReasonPermission},
		{name: "auth level met", p: &maker, opts: []principal.Option{principal.WithAuthLevel(principal.MFADevice)}, status: 200, wantSeen: true},
		{name: "auth level too low", p: &checkerOnlyMFA, opts: []principal.Option{principal.WithAuthLevel(principal.MFADevice)}, status: 403, code: principal.CodeForbidden, reason: principal.ReasonAuthLevel},
		{name: "read-only allowed when not denied", p: &ro, status: 200, wantSeen: true},
		{name: "read-only denied", p: &ro, opts: []principal.Option{principal.DenyReadOnly()}, status: 403, code: principal.CodeForbidden, reason: principal.ReasonReadOnly},
		{name: "wrong realm", p: &maker, opts: []principal.Option{principal.WithRealm(principal.Staff)}, status: 403, code: principal.CodeForbidden, reason: principal.ReasonRealm},
		{name: "right realm", p: &staff, opts: []principal.Option{principal.WithRealm(principal.Staff)}, status: 200, wantSeen: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token := tc.token
			if tc.p != nil {
				token = f.sign(t, *tc.p)
			}
			rec, seen := serve(t, f, token, tc.opts...)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.status, rec.Body)
			}
			if tc.code != "" {
				var body principal.Response
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.ResponseCode != tc.code || body.ResponseDescription != tc.reason {
					t.Errorf("body = %+v, want %s/%s", body, tc.code, tc.reason)
				}
			}
			if (seen != nil) != tc.wantSeen {
				t.Errorf("handler saw principal = %v, want %v", seen != nil, tc.wantSeen)
			}
			if tc.wantSeen && tc.p != nil && seen.OrgID != tc.p.OrgID {
				t.Errorf("handler org = %s, want %s", seen.OrgID, tc.p.OrgID)
			}
		})
	}
}

func TestMiddlewareIgnoresClientSuppliedOrg(t *testing.T) {
	// The org comes from the token. A request that names another org in the
	// query is still served as the token's org.
	f := newFixture(t)
	p := customer()
	e := echo.New()
	var got uuid.UUID
	e.GET("/x", func(c echo.Context) error {
		pr, _ := principal.From(c.Request().Context())
		got = pr.OrgID
		return c.NoContent(http.StatusNoContent)
	}, principal.Require(f.ver))
	req := httptest.NewRequest(http.MethodGet, "/x?org_id="+uuid.NewString(), nil)
	req.Header.Set(principal.HeaderName, f.sign(t, p))
	e.ServeHTTP(httptest.NewRecorder(), req)
	if got != p.OrgID {
		t.Errorf("org = %s, want token's %s", got, p.OrgID)
	}
}

// ---- gRPC interceptor -------------------------------------------------

func call(t *testing.T, f *fixture, ctx context.Context, method string, opts ...principal.Option) (principal.Principal, bool, error) {
	t.Helper()
	var got principal.Principal
	var ok bool
	handler := func(ctx context.Context, _ any) (any, error) {
		got, ok = principal.From(ctx)
		return "done", nil
	}
	_, err := principal.UnaryServerInterceptor(f.ver, opts...)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, handler)
	return got, ok, err
}

func incoming(token string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(principal.MetadataKey, token))
}

func TestGRPCInterceptor(t *testing.T) {
	f := newFixture(t)
	p := customer()
	tok := f.sign(t, p)

	t.Run("valid", func(t *testing.T) {
		got, ok, err := call(t, f, incoming(tok), "/svc/M", principal.WithPerm(principal.PayoutsInitiate))
		if err != nil || !ok || got.OrgID != p.OrgID {
			t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
		}
	})
	t.Run("no metadata at all does not panic", func(t *testing.T) {
		_, ok, err := call(t, f, context.Background(), "/svc/M")
		if status.Code(err) != codes.Unauthenticated || ok {
			t.Fatalf("err = %v ok=%v", err, ok)
		}
	})
	t.Run("metadata without the key does not panic", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-client-id", "abc"))
		_, _, err := call(t, f, ctx, "/svc/M")
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("key present with no values does not panic", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{principal.MetadataKey: {}})
		_, _, err := call(t, f, ctx, "/svc/M")
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("invalid token", func(t *testing.T) {
		_, _, err := call(t, f, incoming("nope"), "/svc/M")
		if status.Code(err) != codes.Unauthenticated || status.Convert(err).Message() != principal.CodeUnauthenticated {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("insufficient permission", func(t *testing.T) {
		_, ok, err := call(t, f, incoming(tok), "/svc/M", principal.WithPerm(principal.ApprovalsDecide))
		if status.Code(err) != codes.PermissionDenied || ok {
			t.Fatalf("err = %v ok=%v", err, ok)
		}
		if msg := status.Convert(err).Message(); !strings.HasPrefix(msg, principal.CodeForbidden) || !strings.Contains(msg, principal.ReasonPermission) {
			t.Errorf("message = %q", msg)
		}
	})
	t.Run("read-only on deny-read-only", func(t *testing.T) {
		ro := customer()
		ro.ReadOnly = true
		_, _, err := call(t, f, incoming(f.sign(t, ro)), "/svc/M", principal.DenyReadOnly())
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("skipped method needs no token", func(t *testing.T) {
		_, _, err := call(t, f, context.Background(), "/grpc.health.v1.Health/Check", principal.SkipMethods("/grpc.health.v1.Health/Check"))
		if err != nil {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("skip does not exempt other methods", func(t *testing.T) {
		_, _, err := call(t, f, context.Background(), "/svc/M", principal.SkipMethods("/grpc.health.v1.Health/Check"))
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestOutgoingContext(t *testing.T) {
	ctx := principal.OutgoingContext(context.Background(), "tok")
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok || len(md.Get(principal.MetadataKey)) != 1 || md.Get(principal.MetadataKey)[0] != "tok" {
		t.Errorf("outgoing md = %v", md)
	}
}

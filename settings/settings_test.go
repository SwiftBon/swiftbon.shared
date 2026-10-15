package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"swiftbon.com/shared/events"
	"swiftbon.com/shared/money"
	"swiftbon.com/shared/principal"
	"swiftbon.com/shared/settings"
)

var t0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// FeeSchedule is a JSON-struct setting.
type FeeSchedule struct {
	Flat  money.Money `json:"flat"`
	Basis int         `json:"basis_points"`
}

type keys struct {
	threshold settings.Key[float64]
	attempts  settings.Key[int64]
	enabled   settings.Key[bool]
	label     settings.Key[string]
	domains   settings.Key[[]string]
	cap       settings.Key[money.Money]
	ttl       settings.Key[time.Duration]
	fees      settings.Key[FeeSchedule]
	limit     settings.Key[int64] // org-scoped
}

type env struct {
	reg   *settings.Registry
	store *settings.MemStore
	mgr   *settings.Manager
	clk   *clock
	k     keys
}

func newEnv(t *testing.T, opts ...settings.ManagerOption) *env {
	t.Helper()
	e := &env{reg: settings.NewRegistry("payouts"), store: settings.NewMemStore(), clk: &clock{t: t0}}
	between := func(lo, hi float64) func(float64) error {
		return func(v float64) error {
			if v < lo || v > hi {
				return fmt.Errorf("%v is outside [%v, %v]", v, lo, hi)
			}
			return nil
		}
	}
	e.k = keys{
		threshold: settings.MustRegister(e.reg, settings.Key[float64]{Name: "payouts.name_match_threshold", Default: 0.85,
			Validate: between(0.5, 1), Description: "name match", Owner: "compliance", Scope: settings.Global, Bounds: "0.5 to 1"}),
		attempts: settings.MustRegister(e.reg, settings.Key[int64]{Name: "payouts.max_attempts", Default: 3,
			Validate: func(v int64) error {
				if v < 1 || v > 10 {
					return errors.New("1 to 10")
				}
				return nil
			}, Description: "attempts", Owner: "treasury", Scope: settings.Global}),
		enabled: settings.MustRegister(e.reg, settings.Key[bool]{Name: "payouts.feature_on", Default: false,
			Validate: func(bool) error { return nil }, Description: "flag", Owner: "commercial", Scope: settings.Global}),
		label: settings.MustRegister(e.reg, settings.Key[string]{Name: "payouts.label", Default: "std",
			Validate: func(s string) error {
				if s == "" {
					return errors.New("empty")
				}
				return nil
			}, Description: "label", Owner: "commercial", Scope: settings.Global}),
		domains: settings.MustRegister(e.reg, settings.Key[[]string]{Name: "payouts.route.NGN", Default: []string{"paylony", "juice"},
			Validate: func(v []string) error {
				if len(v) == 0 {
					return errors.New("at least one provider")
				}
				return nil
			}, Description: "route", Owner: "treasury", Scope: settings.Global}),
		cap: settings.MustRegister(e.reg, settings.Key[money.Money]{Name: "payouts.float_min", Default: money.Money{Amount: 5_000_000_00, Currency: "NGN"},
			Validate: func(m money.Money) error {
				if m.Amount < 0 {
					return errors.New("negative")
				}
				return nil
			}, Description: "float", Owner: "treasury", Scope: settings.Global}),
		ttl: settings.MustRegister(e.reg, settings.Key[time.Duration]{Name: "payouts.name_enquiry_ttl", Default: 15 * time.Minute,
			Validate: func(d time.Duration) error {
				if d <= 0 {
					return errors.New("must be positive")
				}
				return nil
			}, Description: "ttl", Owner: "compliance", Scope: settings.Global}),
		fees: settings.MustRegister(e.reg, settings.Key[FeeSchedule]{Name: "payouts.fee_schedule",
			Default:  FeeSchedule{Flat: money.Money{Amount: 5250, Currency: "NGN"}, Basis: 0},
			Validate: func(f FeeSchedule) error { return f.Flat.Validate() }, Description: "fees", Owner: "commercial", Scope: settings.Org}),
		limit: settings.MustRegister(e.reg, settings.Key[int64]{Name: "payouts.daily_limit", Default: 100,
			Validate: func(v int64) error {
				if v < 0 {
					return errors.New("negative")
				}
				return nil
			}, Description: "limit", Owner: "compliance", Scope: settings.Org}),
	}
	all := append([]settings.ManagerOption{settings.WithClock(e.clk.now)}, opts...)
	e.mgr = settings.NewManager(e.reg, e.store, all...)
	return e
}

func approval() *uuid.UUID { id := uuid.New(); return &id }

func (e *env) apply(t *testing.T, key string, org *uuid.UUID, value string) settings.Row {
	t.Helper()
	row, _, err := e.mgr.Apply(context.Background(), settings.Change{
		Key: key, OrgID: org, Value: json.RawMessage(value), Reason: "tune", ChangedBy: "maker-1", ApprovalRequestID: approval(),
	})
	if err != nil {
		t.Fatalf("apply %s=%s: %v", key, value, err)
	}
	return row
}

// ---- registration ----------------------------------------------------

func TestRegisterRejectsBadDefinitions(t *testing.T) {
	ok := func(float64) error { return nil }
	reg := settings.NewRegistry("payouts")
	tests := []struct {
		name string
		k    settings.Key[float64]
	}{
		{"no validate (no bounds)", settings.Key[float64]{Name: "payouts.a", Description: "d", Owner: "o", Scope: settings.Global}},
		{"no description", settings.Key[float64]{Name: "payouts.a", Validate: ok, Owner: "o", Scope: settings.Global}},
		{"no owner", settings.Key[float64]{Name: "payouts.a", Validate: ok, Description: "d", Scope: settings.Global}},
		{"no scope", settings.Key[float64]{Name: "payouts.a", Validate: ok, Description: "d", Owner: "o"}},
		{"bad name", settings.Key[float64]{Name: "Payouts A", Validate: ok, Description: "d", Owner: "o", Scope: settings.Global}},
		{"no service prefix", settings.Key[float64]{Name: "threshold", Validate: ok, Description: "d", Owner: "o", Scope: settings.Global}},
		{"another service's prefix", settings.Key[float64]{Name: "identity.a", Validate: ok, Description: "d", Owner: "o", Scope: settings.Global}},
		{"default fails own bounds", settings.Key[float64]{Name: "payouts.a", Default: 5, Description: "d", Owner: "o", Scope: settings.Global,
			Validate: func(v float64) error {
				if v > 1 {
					return errors.New("too big")
				}
				return nil
			}}},
		{"NaN default", settings.Key[float64]{Name: "payouts.a", Default: math.NaN(), Validate: ok, Description: "d", Owner: "o", Scope: settings.Global}},
	}
	for _, tc := range tests {
		if _, err := settings.Register(reg, tc.k); !errors.Is(err, settings.ErrInvalidKey) {
			t.Errorf("%s: err = %v, want ErrInvalidKey", tc.name, err)
		}
	}
}

func TestRegisterRejectsUnsupportedTypes(t *testing.T) {
	reg := settings.NewRegistry("payouts")
	if _, err := settings.Register(reg, settings.Key[int]{Name: "payouts.i", Validate: func(int) error { return nil }, Description: "d", Owner: "o", Scope: settings.Global}); !errors.Is(err, settings.ErrInvalidKey) {
		t.Errorf("int accepted: %v", err)
	}
	if _, err := settings.Register(reg, settings.Key[map[string]string]{Name: "payouts.m", Validate: func(map[string]string) error { return nil }, Description: "d", Owner: "o", Scope: settings.Global}); !errors.Is(err, settings.ErrInvalidKey) {
		t.Errorf("map accepted: %v", err)
	}
}

func TestRegisterDuplicateAndMust(t *testing.T) {
	e := newEnv(t)
	_, err := settings.Register(e.reg, e.k.threshold)
	if !errors.Is(err, settings.ErrDuplicateKey) {
		t.Errorf("duplicate: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Error("MustRegister did not panic on a bad key")
		}
	}()
	settings.MustRegister(e.reg, settings.Key[bool]{Name: "bad"})
}

func TestRegistryMeta(t *testing.T) {
	e := newEnv(t)
	if e.reg.Service() != "payouts" {
		t.Error("service")
	}
	names := e.reg.Names()
	if len(names) != 9 || names[0] != "payouts.daily_limit" {
		t.Errorf("names = %v", names)
	}
	if e.mgr.Registry() != e.reg {
		t.Error("Registry()")
	}
}

// ---- reading ---------------------------------------------------------

func TestGetDefaultsWithoutRows(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	f, ver, err := settings.Get(ctx, e.mgr, e.k.threshold, nil)
	if err != nil || f != 0.85 || ver != 0 {
		t.Errorf("float: %v %v %v", f, ver, err)
	}
	i, _, _ := settings.Get(ctx, e.mgr, e.k.attempts, nil)
	b, _, _ := settings.Get(ctx, e.mgr, e.k.enabled, nil)
	s, _, _ := settings.Get(ctx, e.mgr, e.k.label, nil)
	d, _, _ := settings.Get(ctx, e.mgr, e.k.domains, nil)
	m, _, _ := settings.Get(ctx, e.mgr, e.k.cap, nil)
	du, _, _ := settings.Get(ctx, e.mgr, e.k.ttl, nil)
	fs, _, _ := settings.Get(ctx, e.mgr, e.k.fees, nil)
	if i != 3 || b || s != "std" || strings.Join(d, ",") != "paylony,juice" ||
		m != (money.Money{Amount: 500000000, Currency: "NGN"}) || du != 15*time.Minute || fs.Flat.Amount != 5250 {
		t.Errorf("defaults: %v %v %v %v %v %v %v", i, b, s, d, m, du, fs)
	}
}

func TestApplyAndReadBack(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.apply(t, "payouts.name_match_threshold", nil, `0.9`)
	e.apply(t, "payouts.max_attempts", nil, `5`)
	e.apply(t, "payouts.feature_on", nil, `true`)
	e.apply(t, "payouts.label", nil, `"fast"`)
	e.apply(t, "payouts.route.NGN", nil, `["juice"]`)
	e.apply(t, "payouts.float_min", nil, `{"amount":100,"currency":"USD"}`)
	e.apply(t, "payouts.name_enquiry_ttl", nil, `"90s"`)
	e.apply(t, "payouts.fee_schedule", nil, `{"flat":{"amount":100,"currency":"NGN"},"basis_points":25}`)

	f, ver, _ := settings.Get(ctx, e.mgr, e.k.threshold, nil)
	if f != 0.9 || ver != 1 {
		t.Errorf("float = %v v%d", f, ver)
	}
	if i, _, _ := settings.Get(ctx, e.mgr, e.k.attempts, nil); i != 5 {
		t.Errorf("int = %v", i)
	}
	if b, _, _ := settings.Get(ctx, e.mgr, e.k.enabled, nil); !b {
		t.Error("bool")
	}
	if s, _, _ := settings.Get(ctx, e.mgr, e.k.label, nil); s != "fast" {
		t.Errorf("string = %q", s)
	}
	if d, _, _ := settings.Get(ctx, e.mgr, e.k.domains, nil); len(d) != 1 || d[0] != "juice" {
		t.Errorf("slice = %v", d)
	}
	if m, _, _ := settings.Get(ctx, e.mgr, e.k.cap, nil); m != (money.Money{Amount: 100, Currency: "USD"}) {
		t.Errorf("money = %v", m)
	}
	if du, _, _ := settings.Get(ctx, e.mgr, e.k.ttl, nil); du != 90*time.Second {
		t.Errorf("duration = %v", du)
	}
	if fs, _, _ := settings.Get(ctx, e.mgr, e.k.fees, nil); fs.Basis != 25 || fs.Flat.Amount != 100 {
		t.Errorf("struct = %+v", fs)
	}
}

func TestOrgOverridePrecedence(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	orgA, orgB := uuid.New(), uuid.New()

	e.apply(t, "payouts.daily_limit", nil, `200`)  // global value
	e.apply(t, "payouts.daily_limit", &orgA, `50`) // override for A

	get := func(org *uuid.UUID) (int64, settings.Version) {
		v, ver, err := settings.Get(ctx, e.mgr, e.k.limit, org)
		if err != nil {
			t.Fatal(err)
		}
		return v, ver
	}
	if v, ver := get(&orgA); v != 50 || ver != 1 {
		t.Errorf("org A = %d v%d, want override 50 v1", v, ver)
	}
	if v, ver := get(&orgB); v != 200 || ver != 1 {
		t.Errorf("org B = %d v%d, want global 200", v, ver)
	}
	if v, _ := get(nil); v != 200 {
		t.Errorf("no org = %d, want global 200", v)
	}

	// Fall back to the default when neither exists.
	e2 := newEnv(t)
	if v, ver, _ := settings.Get(ctx, e2.mgr, e2.k.limit, &orgA); v != 100 || ver != 0 {
		t.Errorf("default = %d v%d", v, ver)
	}

	// A newer version of the override wins.
	e.apply(t, "payouts.daily_limit", &orgA, `75`)
	if v, ver := get(&orgA); v != 75 || ver != 2 {
		t.Errorf("org A after second override = %d v%d", v, ver)
	}
}

func TestGlobalKeyIgnoresOrg(t *testing.T) {
	e := newEnv(t)
	org := uuid.New()
	e.apply(t, "payouts.max_attempts", nil, `7`)
	v, _, err := settings.Get(context.Background(), e.mgr, e.k.attempts, &org)
	if err != nil || v != 7 {
		t.Errorf("v = %d, err = %v", v, err)
	}
}

func TestGetUnregisteredKey(t *testing.T) {
	e := newEnv(t)
	ghost := settings.Key[int64]{Name: "payouts.ghost", Default: 1}
	if _, _, err := settings.Get(context.Background(), e.mgr, ghost, nil); !errors.Is(err, settings.ErrNotRegistered) {
		t.Errorf("err = %v", err)
	}
	if !settings.IsNotRegistered(fmt.Errorf("wrapped: %w", settings.ErrNotRegistered)) {
		t.Error("IsNotRegistered")
	}
}

func TestGetTypeMismatch(t *testing.T) {
	e := newEnv(t)
	wrong := settings.Key[string]{Name: "payouts.max_attempts"} // registered as int64
	if _, _, err := settings.Get(context.Background(), e.mgr, wrong, nil); !errors.Is(err, settings.ErrTypeMismatch) {
		t.Errorf("err = %v", err)
	}
}

func TestGetFailsLoudlyOnStoredValueOutOfBounds(t *testing.T) {
	e := newEnv(t)
	// A row that was valid when written but violates today's bounds.
	_, _ = e.store.Append(context.Background(), settings.Row{
		Key: "payouts.name_match_threshold", Value: json.RawMessage(`0.1`), EffectiveFrom: t0, ChangedBy: "x", Reason: "old",
	})
	_, ver, err := settings.Get(context.Background(), e.mgr, e.k.threshold, nil)
	if !errors.Is(err, settings.ErrInvalidValue) || ver != 1 {
		t.Errorf("ver=%d err=%v", ver, err)
	}
}

// ---- propose / apply -------------------------------------------------

func TestProposeValidates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	org := uuid.New()

	good, err := e.mgr.Propose(ctx, settings.Change{Key: "payouts.name_match_threshold", Value: json.RawMessage(` 0.90 `), Reason: "tighten"})
	if err != nil {
		t.Fatal(err)
	}
	if string(good.Value) != "0.9" || string(good.Current) != "0.85" || good.CurrentVersion != 0 || good.Owner != "compliance" {
		t.Errorf("proposal = %+v (value %s)", good, good.Value)
	}

	tests := []struct {
		name string
		c    settings.Change
		want error
	}{
		{"unregistered key", settings.Change{Key: "payouts.ghost", Value: json.RawMessage(`1`), Reason: "r"}, settings.ErrNotRegistered},
		{"out of bounds", settings.Change{Key: "payouts.name_match_threshold", Value: json.RawMessage(`0.2`), Reason: "r"}, settings.ErrInvalidValue},
		{"wrong json type", settings.Change{Key: "payouts.name_match_threshold", Value: json.RawMessage(`"high"`), Reason: "r"}, settings.ErrInvalidValue},
		{"garbage json", settings.Change{Key: "payouts.name_match_threshold", Value: json.RawMessage(`{`), Reason: "r"}, settings.ErrInvalidValue},
		{"trailing data", settings.Change{Key: "payouts.name_match_threshold", Value: json.RawMessage(`0.9 1`), Reason: "r"}, settings.ErrInvalidValue},
		{"no reason", settings.Change{Key: "payouts.name_match_threshold", Value: json.RawMessage(`0.9`)}, settings.ErrInvalidValue},
		{"org override on global key", settings.Change{Key: "payouts.name_match_threshold", OrgID: &org, Value: json.RawMessage(`0.9`), Reason: "r"}, settings.ErrScope},
		{"duration as number", settings.Change{Key: "payouts.name_enquiry_ttl", Value: json.RawMessage(`900`), Reason: "r"}, settings.ErrInvalidValue},
		{"duration unparsable", settings.Change{Key: "payouts.name_enquiry_ttl", Value: json.RawMessage(`"soon"`), Reason: "r"}, settings.ErrInvalidValue},
		{"duration not positive", settings.Change{Key: "payouts.name_enquiry_ttl", Value: json.RawMessage(`"-5m"`), Reason: "r"}, settings.ErrInvalidValue},
		{"money bad currency", settings.Change{Key: "payouts.float_min", Value: json.RawMessage(`{"amount":1,"currency":"XXX"}`), Reason: "r"}, settings.ErrInvalidValue},
		{"money unknown field", settings.Change{Key: "payouts.float_min", Value: json.RawMessage(`{"amount":1,"currency":"NGN","x":1}`), Reason: "r"}, settings.ErrInvalidValue},
		{"struct unknown field", settings.Change{Key: "payouts.fee_schedule", Value: json.RawMessage(`{"flat":{"amount":1,"currency":"NGN"},"typo":1}`), Reason: "r"}, settings.ErrInvalidValue},
		{"slice empty", settings.Change{Key: "payouts.route.NGN", Value: json.RawMessage(`[]`), Reason: "r"}, settings.ErrInvalidValue},
	}
	for _, tc := range tests {
		if _, err := e.mgr.Propose(ctx, tc.c); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	// Propose writes nothing.
	if h, _ := e.store.History(ctx, "payouts.name_match_threshold", nil); len(h) != 0 {
		t.Errorf("propose wrote %d rows", len(h))
	}
}

func TestProposeShowsCurrentVersion(t *testing.T) {
	e := newEnv(t)
	e.apply(t, "payouts.max_attempts", nil, `4`)
	p, err := e.mgr.Propose(context.Background(), settings.Change{Key: "payouts.max_attempts", Value: json.RawMessage(`6`), Reason: "r"})
	if err != nil || string(p.Current) != "4" || p.CurrentVersion != 1 {
		t.Errorf("%+v %v", p, err)
	}
}

func TestApplyRequiresApprovalAndActor(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := settings.Change{Key: "payouts.max_attempts", Value: json.RawMessage(`4`), Reason: "r", ChangedBy: "m1", ApprovalRequestID: approval()}

	noApproval := base
	noApproval.ApprovalRequestID = nil
	if _, _, err := e.mgr.Apply(ctx, noApproval); !errors.Is(err, settings.ErrNotApproved) {
		t.Errorf("no approval: %v", err)
	}
	nilApproval := base
	nilApproval.ApprovalRequestID = &uuid.UUID{}
	if _, _, err := e.mgr.Apply(ctx, nilApproval); !errors.Is(err, settings.ErrNotApproved) {
		t.Errorf("nil-uuid approval: %v", err)
	}
	noActor := base
	noActor.ChangedBy = " "
	if _, _, err := e.mgr.Apply(ctx, noActor); !errors.Is(err, settings.ErrInvalidValue) {
		t.Errorf("no actor: %v", err)
	}
	unreg := base
	unreg.Key = "payouts.ghost"
	if _, _, err := e.mgr.Apply(ctx, unreg); !errors.Is(err, settings.ErrNotRegistered) {
		t.Errorf("unregistered: %v", err)
	}
	oob := base
	oob.Value = json.RawMessage(`99`)
	if _, _, err := e.mgr.Apply(ctx, oob); !errors.Is(err, settings.ErrInvalidValue) {
		t.Errorf("out of bounds: %v", err)
	}
	if h, _ := e.store.History(ctx, "payouts.max_attempts", nil); len(h) != 0 {
		t.Errorf("rejected applies wrote %d rows", len(h))
	}
}

func TestApplyReturnsEventAndVersions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	org := uuid.New()
	apr := approval()

	_, ev1, err := e.mgr.Apply(ctx, settings.Change{Key: "payouts.daily_limit", OrgID: &org, Value: json.RawMessage(`10`), Reason: " first ", ChangedBy: "m1", ApprovalRequestID: apr})
	if err != nil {
		t.Fatal(err)
	}
	if ev1.Service != "payouts" || ev1.Key != "payouts.daily_limit" || ev1.Version != 1 || ev1.PreviousVersion != 0 ||
		ev1.OrgID != org || ev1.ChangedBy != "m1" || *ev1.ApprovalRequestID != *apr || ev1.Reason != "first" || !ev1.EffectiveFrom.Equal(t0) {
		t.Errorf("event = %+v", ev1)
	}
	if ev1.TopicName() != events.TopicSettingChanged {
		t.Error("topic")
	}
	_, ev2, _ := e.mgr.Apply(ctx, settings.Change{Key: "payouts.daily_limit", OrgID: &org, Value: json.RawMessage(`20`), Reason: "r", ChangedBy: "m1", ApprovalRequestID: apr})
	if ev2.Version != 2 || ev2.PreviousVersion != 1 {
		t.Errorf("second event = %+v", ev2)
	}
	_, evG, _ := e.mgr.Apply(ctx, settings.Change{Key: "payouts.max_attempts", Value: json.RawMessage(`2`), Reason: "r", ChangedBy: "m1", ApprovalRequestID: apr})
	if evG.OrgID != uuid.Nil {
		t.Errorf("global change carries org %s", evG.OrgID)
	}
}

func TestApprovalCheckHook(t *testing.T) {
	var seen settings.Change
	deny := errors.New("approval does not match")
	e := newEnv(t, settings.WithApprovalCheck(func(_ context.Context, c settings.Change) error {
		seen = c
		if string(c.Value) == "9" {
			return deny
		}
		return nil
	}))
	ctx := context.Background()
	c := settings.Change{Key: "payouts.max_attempts", Value: json.RawMessage(`4`), Reason: "r", ChangedBy: "m", ApprovalRequestID: approval()}
	if _, _, err := e.mgr.Apply(ctx, c); err != nil || seen.Key != c.Key {
		t.Errorf("allowed: %v", err)
	}
	c.Value = json.RawMessage(`9`)
	if _, _, err := e.mgr.Apply(ctx, c); !errors.Is(err, deny) {
		t.Errorf("denied: %v", err)
	}
}

func TestEffectiveFromInFuture(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, _, err := e.mgr.Apply(ctx, settings.Change{
		Key: "payouts.max_attempts", Value: json.RawMessage(`8`), Reason: "r", ChangedBy: "m",
		ApprovalRequestID: approval(), EffectiveFrom: t0.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, _, _ := settings.Get(ctx, e.mgr, e.k.attempts, nil); v != 3 {
		t.Errorf("before effective time = %d, want default 3", v)
	}
	e.clk.advance(2 * time.Hour)
	if v, ver, _ := settings.Get(ctx, e.mgr, e.k.attempts, nil); v != 8 || ver != 1 {
		t.Errorf("after effective time = %d v%d", v, ver)
	}
}

func TestApplyThroughAnotherStore(t *testing.T) {
	e := newEnv(t)
	tx := settings.NewMemStore() // stands in for a transaction-bound store
	_, _, err := e.mgr.Apply(context.Background(), settings.Change{
		Key: "payouts.max_attempts", Value: json.RawMessage(`4`), Reason: "r", ChangedBy: "m", ApprovalRequestID: approval(),
	}, settings.UsingStore(tx))
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := tx.History(context.Background(), "payouts.max_attempts", nil); len(h) != 1 {
		t.Errorf("tx store has %d rows", len(h))
	}
	if h, _ := e.store.History(context.Background(), "payouts.max_attempts", nil); len(h) != 0 {
		t.Errorf("main store has %d rows", len(h))
	}
}

// ---- cache -----------------------------------------------------------

func TestCacheTTLAndInvalidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.apply(t, "payouts.max_attempts", nil, `4`) // Apply invalidates, so the next Get loads
	if v, _, _ := settings.Get(ctx, e.mgr, e.k.attempts, nil); v != 4 {
		t.Fatalf("v = %d", v)
	}

	// Another replica writes directly to the store; this replica keeps its cache...
	_, _ = e.store.Append(ctx, settings.Row{Key: "payouts.max_attempts", Value: json.RawMessage(`6`), EffectiveFrom: t0, ChangedBy: "other", Reason: "r"})
	e.clk.advance(30 * time.Second)
	if v, _, _ := settings.Get(ctx, e.mgr, e.k.attempts, nil); v != 4 {
		t.Errorf("within TTL = %d, want cached 4", v)
	}

	// ...until a setting-changed event for this service arrives.
	e.mgr.OnSettingChanged(events.SettingChanged{Service: "identity", Key: "identity.x"})
	if v, _, _ := settings.Get(ctx, e.mgr, e.k.attempts, nil); v != 4 {
		t.Errorf("another service's event busted the cache: %d", v)
	}
	e.mgr.OnSettingChanged(events.SettingChanged{Service: "payouts", Key: "payouts.max_attempts"})
	if v, _, _ := settings.Get(ctx, e.mgr, e.k.attempts, nil); v != 6 {
		t.Errorf("after event = %d, want 6", v)
	}

	// And with no event at all, the snapshot expires after at most 60s.
	_, _ = e.store.Append(ctx, settings.Row{Key: "payouts.max_attempts", Value: json.RawMessage(`7`), EffectiveFrom: t0, ChangedBy: "other", Reason: "r"})
	e.clk.advance(61 * time.Second)
	if v, _, _ := settings.Get(ctx, e.mgr, e.k.attempts, nil); v != 7 {
		t.Errorf("after TTL = %d, want 7", v)
	}
}

func TestCacheTTLIsCapped(t *testing.T) {
	e := newEnv(t, settings.WithCacheTTL(time.Hour))
	ctx := context.Background()
	_, _, _ = settings.Get(ctx, e.mgr, e.k.attempts, nil)
	_, _ = e.store.Append(ctx, settings.Row{Key: "payouts.max_attempts", Value: json.RawMessage(`6`), EffectiveFrom: t0, ChangedBy: "o", Reason: "r"})
	e.clk.advance(61 * time.Second)
	if v, _, _ := settings.Get(ctx, e.mgr, e.k.attempts, nil); v != 6 {
		t.Errorf("a 1h TTL was honoured: %d", v)
	}
}

type flakyStore struct {
	*settings.MemStore
	mu   sync.Mutex
	fail bool
}

func (f *flakyStore) Current(ctx context.Context, now time.Time) ([]settings.Row, error) {
	f.mu.Lock()
	fail := f.fail
	f.mu.Unlock()
	if fail {
		return nil, errors.New("db down")
	}
	return f.MemStore.Current(ctx, now)
}

func TestStoreOutageServesStaleThenErrors(t *testing.T) {
	reg := settings.NewRegistry("payouts")
	k := settings.MustRegister(reg, settings.Key[int64]{Name: "payouts.n", Default: 1, Validate: func(int64) error { return nil }, Description: "d", Owner: "o", Scope: settings.Global})
	fs := &flakyStore{MemStore: settings.NewMemStore()}
	clk := &clock{t: t0}
	m := settings.NewManager(reg, fs, settings.WithClock(clk.now))
	ctx := context.Background()

	// No snapshot yet and the store is down: an error, not a silent default.
	fs.fail = true
	if _, _, err := settings.Get(ctx, m, k, nil); err == nil {
		t.Fatal("expected an error with no snapshot and the store down")
	}
	fs.fail = false
	if _, _, err := settings.Get(ctx, m, k, nil); err != nil {
		t.Fatal(err)
	}
	// Snapshot exists; the store fails after it expires: the stale value is served.
	fs.fail = true
	clk.advance(2 * time.Minute)
	if v, _, err := settings.Get(ctx, m, k, nil); err != nil || v != 1 {
		t.Errorf("stale = %d, %v", v, err)
	}
}

// ---- seeding ---------------------------------------------------------

func TestSeedDoesNotOverwrite(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	n, err := e.mgr.Seed(ctx)
	if err != nil || n != 9 {
		t.Fatalf("first seed inserted %d, err %v", n, err)
	}
	h, _ := e.store.History(ctx, "payouts.name_match_threshold", nil)
	if len(h) != 1 || h[0].ChangedBy != settings.SeedActor || h[0].Version != 1 {
		t.Fatalf("seed row = %+v", h)
	}
	if v, ver, _ := settings.Get(ctx, e.mgr, e.k.threshold, nil); v != 0.85 || ver != 1 {
		t.Errorf("seeded read = %v v%d", v, ver)
	}

	// An approved change, then a "redeploy" with a different config default.
	e.apply(t, "payouts.name_match_threshold", nil, `0.95`)
	reg2 := settings.NewRegistry("payouts")
	settings.MustRegister(reg2, settings.Key[float64]{Name: "payouts.name_match_threshold", Default: 0.7,
		Validate: func(float64) error { return nil }, Description: "d", Owner: "o", Scope: settings.Global})
	m2 := settings.NewManager(reg2, e.store, settings.WithClock(e.clk.now))
	n, err = m2.Seed(ctx)
	if err != nil || n != 0 {
		t.Fatalf("second seed inserted %d, err %v", n, err)
	}
	h, _ = e.store.History(ctx, "payouts.name_match_threshold", nil)
	if len(h) != 2 || string(h[0].Value) != "0.95" {
		t.Errorf("history after reseed = %+v", h)
	}
}

type failingStore struct{ *settings.MemStore }

func (failingStore) Seed(context.Context, settings.Row) (bool, error) {
	return false, errors.New("boom")
}
func (failingStore) Append(context.Context, settings.Row) (settings.Row, error) {
	return settings.Row{}, errors.New("boom")
}
func (failingStore) History(context.Context, string, *uuid.UUID) ([]settings.Row, error) {
	return nil, errors.New("boom")
}

func TestStoreErrorsSurface(t *testing.T) {
	e := newEnv(t)
	m := settings.NewManager(e.reg, failingStore{settings.NewMemStore()}, settings.WithClock(e.clk.now))
	ctx := context.Background()
	if _, err := m.Seed(ctx); err == nil {
		t.Error("seed error swallowed")
	}
	c := settings.Change{Key: "payouts.max_attempts", Value: json.RawMessage(`4`), Reason: "r", ChangedBy: "m", ApprovalRequestID: approval()}
	if _, _, err := m.Apply(ctx, c); err == nil {
		t.Error("history error swallowed")
	}
	if _, err := m.Inspect(ctx, "payouts.max_attempts", nil); err == nil {
		t.Error("inspect history error swallowed")
	}
}

// ---- describe / inspect ----------------------------------------------

func TestListAndInspect(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	org := uuid.New()
	e.apply(t, "payouts.daily_limit", nil, `200`)
	e.apply(t, "payouts.daily_limit", nil, `300`)
	e.apply(t, "payouts.daily_limit", &org, `50`)

	list, err := e.mgr.List(ctx)
	if err != nil || len(list) != 9 {
		t.Fatalf("list: %d, %v", len(list), err)
	}
	var limit settings.Description
	for _, d := range list {
		if d.Key == "payouts.daily_limit" {
			limit = d
		}
	}
	if string(limit.Value) != "300" || limit.Version != 2 || string(limit.Default) != "100" || limit.Scope != settings.Org || limit.Owner != "compliance" || limit.LastChangedBy != "maker-1" {
		t.Errorf("limit = %+v", limit)
	}

	d, err := e.mgr.Inspect(ctx, "payouts.daily_limit", nil)
	if err != nil || len(d.History) != 2 || d.History[0].Version != 2 || string(d.History[1].Value) != "200" {
		t.Errorf("global detail = %+v, %v", d, err)
	}
	d, err = e.mgr.Inspect(ctx, "payouts.daily_limit", &org)
	if err != nil || string(d.Value) != "50" || len(d.History) != 1 || d.OrgID == nil {
		t.Errorf("org detail = %+v, %v", d, err)
	}
	if _, err := e.mgr.Inspect(ctx, "payouts.ghost", nil); !errors.Is(err, settings.ErrNotRegistered) {
		t.Errorf("ghost: %v", err)
	}
	if _, err := e.mgr.Inspect(ctx, "payouts.max_attempts", &org); !errors.Is(err, settings.ErrScope) {
		t.Errorf("org on global: %v", err)
	}
}

func TestMigrationTemplate(t *testing.T) {
	for _, want := range []string{"scope_org_id", "approval_request_id", "COALESCE(scope_org_id", "settings_immutable", "UNIQUE (key, scope_org_id, version)"} {
		if !strings.Contains(settings.MigrationUp, want) {
			t.Errorf("MigrationUp lacks %q", want)
		}
	}
	if !strings.Contains(settings.MigrationDown, "DROP TABLE") {
		t.Error("MigrationDown")
	}
}

// ---- HTTP ------------------------------------------------------------

type api struct {
	e    *env
	echo *echo.Echo
}

func newAPI(t *testing.T, mw ...echo.MiddlewareFunc) *api {
	t.Helper()
	e := newEnv(t)
	srv := echo.New()
	settings.Mount(srv.Group("/ops/settings", mw...), e.mgr)
	return &api{e: e, echo: srv}
}

func (a *api) do(t *testing.T, method, path, body string, headers ...string) (int, settings.Result, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	a.echo.ServeHTTP(rec, req)
	var res settings.Result
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	details, _ := res.ResponseDetails.(map[string]any)
	return rec.Code, res, details
}

func TestHTTPFlow(t *testing.T) {
	a := newAPI(t)

	code, res, _ := a.do(t, http.MethodGet, "/ops/settings", "")
	list, _ := res.ResponseDetails.([]any)
	if code != 200 || res.ResponseCode != settings.CodeSuccess || len(list) != 9 {
		t.Fatalf("list: %d %+v", code, res)
	}

	_, res, d := a.do(t, http.MethodGet, "/ops/settings/payouts.max_attempts", "")
	if res.ResponseCode != "00" || d["key"] != "payouts.max_attempts" {
		t.Errorf("get: %+v", res)
	}
	_, res, _ = a.do(t, http.MethodGet, "/ops/settings/payouts.ghost", "")
	if res.ResponseCode != settings.CodeNotRegistered {
		t.Errorf("get ghost: %+v", res)
	}
	_, res, _ = a.do(t, http.MethodGet, "/ops/settings/payouts.max_attempts?org_id=nope", "")
	if res.ResponseCode != settings.CodeValidation {
		t.Errorf("bad org: %+v", res)
	}
	_, res, _ = a.do(t, http.MethodGet, "/ops/settings/payouts.max_attempts?org_id="+uuid.NewString(), "")
	if res.ResponseCode != settings.CodeScope {
		t.Errorf("org on global key: %+v", res)
	}

	_, res, d = a.do(t, http.MethodPost, "/ops/settings/payouts.max_attempts/propose", `{"value":5,"reason":"raise"}`)
	if res.ResponseCode != "00" || d["Value"] == nil {
		t.Errorf("propose: %+v", res)
	}
	_, res, _ = a.do(t, http.MethodPost, "/ops/settings/payouts.max_attempts/propose", `{"value":50,"reason":"raise"}`)
	if res.ResponseCode != settings.CodeInvalidValue {
		t.Errorf("propose out of bounds: %+v", res)
	}
	_, res, _ = a.do(t, http.MethodPost, "/ops/settings/payouts.max_attempts/propose", `not json`)
	if res.ResponseCode != settings.CodeValidation {
		t.Errorf("propose bad body: %+v", res)
	}

	_, res, _ = a.do(t, http.MethodPost, "/ops/settings/payouts.max_attempts/apply", `{"value":5,"reason":"raise","changed_by":"m1"}`)
	if res.ResponseCode != settings.CodeApprovalRequired {
		t.Errorf("apply without approval: %+v", res)
	}
	_, res, _ = a.do(t, http.MethodPost, "/ops/settings/payouts.max_attempts/apply", `oops`)
	if res.ResponseCode != settings.CodeValidation {
		t.Errorf("apply bad body: %+v", res)
	}
	body := fmt.Sprintf(`{"value":5,"reason":"raise","changed_by":"m1","approval_request_id":%q}`, uuid.NewString())
	_, res, d = a.do(t, http.MethodPost, "/ops/settings/payouts.max_attempts/apply", body)
	if res.ResponseCode != "00" || d["version"] != float64(1) {
		t.Errorf("apply: %+v", res)
	}
	v, _, _ := settings.Get(context.Background(), a.e.mgr, a.e.k.attempts, nil)
	if v != 5 {
		t.Errorf("value after apply = %d", v)
	}
}

func TestHTTPApplyTakesActorFromPrincipal(t *testing.T) {
	a := newAPI(t)
	staff := uuid.New()
	a.echo = echo.New()
	a.echo.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.SetRequest(c.Request().WithContext(principal.WithContext(c.Request().Context(), principal.Principal{UserID: staff, Realm: principal.Staff})))
			return next(c)
		}
	})
	settings.Mount(a.echo.Group("/ops/settings"), a.e.mgr)

	body := fmt.Sprintf(`{"value":4,"reason":"r","changed_by":"spoofed","approval_request_id":%q}`, uuid.NewString())
	_, res, _ := a.do(t, http.MethodPost, "/ops/settings/payouts.max_attempts/apply", body)
	if res.ResponseCode != "00" {
		t.Fatalf("apply: %+v", res)
	}
	h, _ := a.e.store.History(context.Background(), "payouts.max_attempts", nil)
	if len(h) != 1 || h[0].ChangedBy != staff.String() {
		t.Errorf("changed_by = %+v, want the authenticated staff id", h)
	}
}

func TestHTTPInternalError(t *testing.T) {
	e := newEnv(t)
	m := settings.NewManager(e.reg, failingCurrentStore{settings.NewMemStore()}, settings.WithClock(e.clk.now))
	srv := echo.New()
	settings.Mount(srv.Group("/s"), m)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s", nil))
	var res settings.Result
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.ResponseCode != settings.CodeInternal {
		t.Errorf("res = %+v", res)
	}
}

type failingCurrentStore struct{ *settings.MemStore }

func (failingCurrentStore) Current(context.Context, time.Time) ([]settings.Row, error) {
	return nil, errors.New("db down")
}

func TestHTTPMiddlewareGuardsRoutes(t *testing.T) {
	// Mounting under principal.Require refuses a caller with no token.
	e := newEnv(t)
	ver, _ := principal.NewVerifier("payouts", principal.KeySet{})
	srv := echo.New()
	settings.Mount(srv.Group("/s", principal.Require(ver, principal.WithRealm(principal.Staff))), e.mgr)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d", rec.Code)
	}
}

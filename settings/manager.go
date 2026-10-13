package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"swiftbon.com/shared/events"
)

// MaxCacheTTL is the longest a snapshot may be served. A change is therefore
// effective on every replica within this time even if a setting-changed event
// is missed.
const MaxCacheTTL = 60 * time.Second

// Manager reads, proposes and applies the settings in one Registry.
type Manager struct {
	reg           *Registry
	store         Store
	ttl           time.Duration
	now           func() time.Time
	approvalCheck func(ctx context.Context, c Change) error

	mu     sync.Mutex
	snap   map[snapKey]Row
	loaded time.Time
	valid  bool
}

type snapKey struct {
	key string
	org uuid.UUID // uuid.Nil for the global row
}

func keyOf(name string, org *uuid.UUID) snapKey {
	if org == nil {
		return snapKey{key: name}
	}
	return snapKey{key: name, org: *org}
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithCacheTTL sets how long a snapshot is served. It is capped at MaxCacheTTL.
func WithCacheTTL(d time.Duration) ManagerOption {
	return func(m *Manager) { m.ttl = min(d, MaxCacheTTL) }
}

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) ManagerOption {
	return func(m *Manager) { m.now = now }
}

// WithApprovalCheck makes Apply confirm with approvals that the change was
// approved, and that the approved payload is the one being applied, before it
// writes. Without it Apply only requires an approval id to be present.
func WithApprovalCheck(check func(ctx context.Context, c Change) error) ManagerOption {
	return func(m *Manager) { m.approvalCheck = check }
}

// NewManager returns a Manager over reg and store.
func NewManager(reg *Registry, store Store, opts ...ManagerOption) *Manager {
	m := &Manager{reg: reg, store: store, ttl: MaxCacheTTL, now: time.Now}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Registry returns the registry m reads.
func (m *Manager) Registry() *Registry { return m.reg }

// Seed inserts, for every registered key, a global row from its default if none
// exists, with changed_by "seed". Existing rows are never overwritten, so a
// value changed through approval survives a redeploy with a different config
// default. Call it once at boot. It returns how many rows it inserted.
func (m *Manager) Seed(ctx context.Context) (int, error) {
	inserted := 0
	for _, name := range m.reg.Names() {
		e, _ := m.reg.lookup(name)
		ok, err := m.store.Seed(ctx, Row{
			Key: name, Value: e.defaultJSON, EffectiveFrom: m.now().UTC(),
			ChangedBy: SeedActor, Reason: "seeded from the registered default",
		})
		if err != nil {
			return inserted, fmt.Errorf("settings: seed %s: %w", name, err)
		}
		if ok {
			inserted++
		}
	}
	m.Invalidate()
	return inserted, nil
}

// Invalidate drops the cached snapshot so the next read reloads it.
func (m *Manager) Invalidate() {
	m.mu.Lock()
	m.valid = false
	m.mu.Unlock()
}

// OnSettingChanged is the setting-changed event handler: it busts the cache
// when the change is to one of this service's keys.
func (m *Manager) OnSettingChanged(ev events.SettingChanged) {
	if ev.Service == m.reg.service {
		m.Invalidate()
	}
}

// snapshot returns the current rows, reloading when stale. If a reload fails
// and an older snapshot exists, the older one is served: a store blip must not
// stop payouts, and the staleness is bounded by the next successful reload.
func (m *Manager) snapshot(ctx context.Context) (map[snapKey]Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if m.valid && now.Sub(m.loaded) < m.ttl {
		return m.snap, nil
	}
	rows, err := m.store.Current(ctx, now)
	if err != nil {
		if m.snap != nil {
			return m.snap, nil
		}
		return nil, fmt.Errorf("settings: load: %w", err)
	}
	snap := make(map[snapKey]Row, len(rows))
	for _, r := range rows {
		snap[keyOf(r.Key, r.OrgID)] = r
	}
	m.snap, m.loaded, m.valid = snap, now, true
	return snap, nil
}

// resolve finds the row that applies: an organisation override (for Org-scoped
// keys), then the global row. It returns nil when only the default applies.
func resolve(e *entry, snap map[snapKey]Row, org *uuid.UUID) *Row {
	if e.scope == Org && org != nil {
		if r, ok := snap[keyOf(e.name, org)]; ok {
			return &r
		}
	}
	if r, ok := snap[keyOf(e.name, nil)]; ok {
		return &r
	}
	return nil
}

// Get returns the value of key for orgID, with the version it came from. It
// resolves an organisation override, then the global value, then the registered
// default; Version 0 means the default. A request that depends on a setting
// should record the Version it was handed.
//
// orgID is ignored for Global keys. A key that is not registered with m, or is
// registered with a different type, is an error rather than a zero value.
func Get[T any](ctx context.Context, m *Manager, key Key[T], orgID *uuid.UUID) (T, Version, error) {
	var zero T
	e, err := m.reg.lookup(key.Name)
	if err != nil {
		return zero, 0, err
	}
	if e.typ != reflect.TypeFor[T]() {
		return zero, 0, fmt.Errorf("%w: %s is %s", ErrTypeMismatch, key.Name, e.typ)
	}
	snap, err := m.snapshot(ctx)
	if err != nil {
		return zero, 0, err
	}
	raw, ver := e.defaultJSON, Version(0)
	if r := resolve(e, snap, orgID); r != nil {
		raw, ver = r.Value, r.Version
	}
	v, err := e.decode(raw)
	if err != nil {
		return zero, ver, fmt.Errorf("settings: stored value of %s (version %d): %w", key.Name, ver, err)
	}
	return v.(T), ver, nil
}

// Change describes a requested change to one setting.
type Change struct {
	Key   string
	OrgID *uuid.UUID
	// Value is the new value as JSON: 0.9, "15m", {"amount":500000,"currency":"NGN"}.
	Value  json.RawMessage
	Reason string
	// ChangedBy identifies the maker (a user id, or SeedActor). Required on Apply.
	ChangedBy string
	// ApprovalRequestID is the approved maker-checker request. Required on Apply.
	ApprovalRequestID *uuid.UUID
	// EffectiveFrom defaults to now.
	EffectiveFrom time.Time
}

// Proposal is a validated change, ready to put in an approval request.
type Proposal struct {
	Key         string
	OrgID       *uuid.UUID
	Description string
	Owner       string
	// Value is the canonical JSON of the proposed value.
	Value json.RawMessage
	// Current is the value in force for the same scope, and its version.
	Current        json.RawMessage
	CurrentVersion Version
	Reason         string
}

// Propose validates c against the registered type and bounds and returns the
// canonical proposal. It writes nothing, and it is called before the approval is
// opened, so an out-of-bounds value never reaches an approver.
func (m *Manager) Propose(ctx context.Context, c Change) (Proposal, error) {
	e, err := m.validateChange(c)
	if err != nil {
		return Proposal{}, err
	}
	norm, err := e.normalise(c.Value)
	if err != nil {
		return Proposal{}, err
	}
	snap, err := m.snapshot(ctx)
	if err != nil {
		return Proposal{}, err
	}
	cur, ver := e.defaultJSON, Version(0)
	if r := resolve(e, snap, c.OrgID); r != nil {
		cur, ver = r.Value, r.Version
	}
	return Proposal{
		Key: e.name, OrgID: c.OrgID, Description: e.description, Owner: e.owner,
		Value: norm, Current: cur, CurrentVersion: ver, Reason: strings.TrimSpace(c.Reason),
	}, nil
}

func (m *Manager) validateChange(c Change) (*entry, error) {
	e, err := m.reg.lookup(c.Key)
	if err != nil {
		return nil, err
	}
	if c.OrgID != nil && e.scope != Org {
		return nil, fmt.Errorf("%w: %s", ErrScope, c.Key)
	}
	if strings.TrimSpace(c.Reason) == "" {
		return nil, fmt.Errorf("%w: a reason is required", ErrInvalidValue)
	}
	return e, nil
}

// ApplyOption adjusts one Apply call.
type ApplyOption func(*applyConfig)

type applyConfig struct{ store Store }

// UsingStore makes Apply write through st instead of the Manager's store. Pass a
// store bound to the caller's open transaction so the new version and the
// setting-changed outbox row commit together.
func UsingStore(st Store) ApplyOption { return func(c *applyConfig) { c.store = st } }

// Apply writes c as the next version of its setting, after approval. It
// re-validates (a bound may have tightened since the proposal), refuses a change
// with no approval, and returns the new row and the setting-changed event the
// caller must publish. It cannot change an unregistered key.
func (m *Manager) Apply(ctx context.Context, c Change, opts ...ApplyOption) (Row, events.SettingChanged, error) {
	cfg := applyConfig{store: m.store}
	for _, o := range opts {
		o(&cfg)
	}
	e, err := m.validateChange(c)
	if err != nil {
		return Row{}, events.SettingChanged{}, err
	}
	if c.ApprovalRequestID == nil || *c.ApprovalRequestID == uuid.Nil {
		return Row{}, events.SettingChanged{}, ErrNotApproved
	}
	if strings.TrimSpace(c.ChangedBy) == "" {
		return Row{}, events.SettingChanged{}, fmt.Errorf("%w: changed_by is required", ErrInvalidValue)
	}
	norm, err := e.normalise(c.Value)
	if err != nil {
		return Row{}, events.SettingChanged{}, err
	}
	if m.approvalCheck != nil {
		if err := m.approvalCheck(ctx, c); err != nil {
			return Row{}, events.SettingChanged{}, fmt.Errorf("settings: approval check: %w", err)
		}
	}
	effective := c.EffectiveFrom
	if effective.IsZero() {
		effective = m.now()
	}

	prev := int64(0)
	if hist, err := cfg.store.History(ctx, e.name, c.OrgID); err != nil {
		return Row{}, events.SettingChanged{}, fmt.Errorf("settings: history: %w", err)
	} else if len(hist) > 0 {
		prev = int64(hist[0].Version)
	}

	row, err := cfg.store.Append(ctx, Row{
		Key: e.name, OrgID: c.OrgID, Value: norm, EffectiveFrom: effective.UTC(),
		ChangedBy: c.ChangedBy, ApprovalRequestID: c.ApprovalRequestID, Reason: strings.TrimSpace(c.Reason),
	})
	if err != nil {
		return Row{}, events.SettingChanged{}, fmt.Errorf("settings: append: %w", err)
	}
	m.Invalidate()

	ev := events.SettingChanged{
		Service: m.reg.service, Key: e.name, Version: int64(row.Version), PreviousVersion: prev,
		ChangedBy: c.ChangedBy, ApprovalRequestID: c.ApprovalRequestID, Reason: row.Reason,
		EffectiveFrom: row.EffectiveFrom,
	}
	if c.OrgID != nil {
		ev.OrgID = *c.OrgID
	}
	return row, ev, nil
}

// Description is one setting as ops sees it.
type Description struct {
	Key         string          `json:"key"`
	Type        string          `json:"type"`
	Scope       Scope           `json:"scope"`
	Description string          `json:"description"`
	Owner       string          `json:"owner"`
	Bounds      string          `json:"bounds,omitempty"`
	Default     json.RawMessage `json:"default"`
	Value       json.RawMessage `json:"value"`
	// Version is 0 when the default is in force.
	Version       Version    `json:"version"`
	LastChangedAt *time.Time `json:"last_changed_at,omitempty"`
	LastChangedBy string     `json:"last_changed_by,omitempty"`
}

func describe(e *entry, snap map[snapKey]Row, org *uuid.UUID) Description {
	d := Description{
		Key: e.name, Type: e.typeName, Scope: e.scope, Description: e.description,
		Owner: e.owner, Bounds: e.bounds, Default: e.defaultJSON, Value: e.defaultJSON,
	}
	if r := resolve(e, snap, org); r != nil {
		at := r.CreatedAt
		d.Value, d.Version, d.LastChangedAt, d.LastChangedBy = r.Value, r.Version, &at, r.ChangedBy
	}
	return d
}

// List describes every registered key at its global value.
func (m *Manager) List(ctx context.Context) ([]Description, error) {
	snap, err := m.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	names := m.reg.Names()
	out := make([]Description, 0, len(names))
	for _, n := range names {
		e, _ := m.reg.lookup(n)
		out = append(out, describe(e, snap, nil))
	}
	return out, nil
}

// Detail is one setting with its history.
type Detail struct {
	Description
	OrgID *uuid.UUID `json:"org_id,omitempty"`
	// History lists every version of the requested scope, newest first.
	History []HistoryEntry `json:"history"`
}

// HistoryEntry is one stored version, as returned to ops.
type HistoryEntry struct {
	Version           Version         `json:"version"`
	Value             json.RawMessage `json:"value"`
	EffectiveFrom     time.Time       `json:"effective_from"`
	ChangedBy         string          `json:"changed_by"`
	ApprovalRequestID *uuid.UUID      `json:"approval_request_id,omitempty"`
	Reason            string          `json:"reason"`
	CreatedAt         time.Time       `json:"created_at"`
}

// Inspect returns key's effective value for orgID (the global value when orgID
// is nil) and the stored history for that scope.
func (m *Manager) Inspect(ctx context.Context, key string, orgID *uuid.UUID) (Detail, error) {
	e, err := m.reg.lookup(key)
	if err != nil {
		return Detail{}, err
	}
	if orgID != nil && e.scope != Org {
		return Detail{}, fmt.Errorf("%w: %s", ErrScope, key)
	}
	snap, err := m.snapshot(ctx)
	if err != nil {
		return Detail{}, err
	}
	hist, err := m.store.History(ctx, key, orgID)
	if err != nil {
		return Detail{}, fmt.Errorf("settings: history: %w", err)
	}
	d := Detail{Description: describe(e, snap, orgID), OrgID: orgID, History: make([]HistoryEntry, 0, len(hist))}
	for _, r := range hist {
		d.History = append(d.History, HistoryEntry{
			Version: r.Version, Value: r.Value, EffectiveFrom: r.EffectiveFrom, ChangedBy: r.ChangedBy,
			ApprovalRequestID: r.ApprovalRequestID, Reason: r.Reason, CreatedAt: r.CreatedAt,
		})
	}
	return d, nil
}

// IsNotRegistered reports whether err means the key is not registered.
func IsNotRegistered(err error) bool { return errors.Is(err, ErrNotRegistered) }

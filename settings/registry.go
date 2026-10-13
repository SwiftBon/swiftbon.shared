// Package settings is the one implementation of runtime-configurable values.
//
// Every threshold, window, limit, fee table and routing order that compliance,
// treasury or a commercial team may tune is a setting: registered in code with
// a type, a default, bounds and an owner; stored as versioned rows; changed
// only through an approval; effective within a minute with no deploy. Without a
// shared implementation, ten services would build ten slightly different ones.
//
// The flow:
//
//	reg := settings.NewRegistry("payouts")
//	var NameMatchThreshold, _ = settings.Register(reg, settings.Key[float64]{
//	    Name: "payouts.name_match_threshold", Default: 0.85, Scope: settings.Global,
//	    Description: "...", Owner: "compliance", Bounds: "0.5 to 1",
//	    Validate: func(v float64) error { ... },
//	})
//	m := settings.NewManager(reg, store)
//	m.Seed(ctx)                                  // at boot
//	v, ver, err := settings.Get(ctx, m, NameMatchThreshold, &orgID)
//
// Money is never a float64 setting: use Key[money.Money].
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"swiftbon.com/shared/money"
)

// Scope says whether a setting may be overridden per organisation.
type Scope string

const (
	// Global settings have one value for the platform.
	Global Scope = "global"
	// Org settings have a global value and optional per-organisation overrides
	// (limits, fees, approval thresholds).
	Org Scope = "org"
)

// Version identifies a stored value. Zero means no row exists and the
// registered default was used.
type Version int64

// Errors returned by the package.
var (
	ErrNotRegistered = errors.New("settings: key is not registered")
	ErrInvalidValue  = errors.New("settings: value is not valid for this key")
	ErrInvalidKey    = errors.New("settings: key definition is invalid")
	ErrScope         = errors.New("settings: organisation override on a global key")
	ErrTypeMismatch  = errors.New("settings: key type does not match its registration")
	ErrNotApproved   = errors.New("settings: change has no approval")
	ErrDuplicateKey  = errors.New("settings: key already registered")
)

// Key is the definition of one setting. All of Name, Validate, Description and
// Owner are required: a key without bounds is a key anyone can set to anything.
type Key[T any] struct {
	// Name is "<service>.<setting>", for example "payouts.name_match_threshold".
	Name    string
	Default T
	// Validate is the registered bounds. It runs on the default at registration,
	// on every proposal before an approval is opened, and again on apply.
	Validate    func(T) error
	Description string
	// Owner is the team that tunes it (compliance, treasury, commercial).
	Owner string
	Scope Scope
	// Bounds is a human description of what Validate allows, shown to ops.
	Bounds string
}

var keyName = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[A-Za-z0-9_]+)+$`)

// entry is the untyped record the registry keeps for a key.
type entry struct {
	name        string
	scope       Scope
	description string
	owner       string
	bounds      string
	typ         reflect.Type
	typeName    string
	defaultJSON json.RawMessage
	// normalise decodes raw strictly, validates it, and re-encodes it canonically.
	normalise func(raw json.RawMessage) (json.RawMessage, error)
	// decode decodes raw strictly and validates it, returning the typed value.
	decode func(raw json.RawMessage) (any, error)
}

// Registry holds the keys one service owns.
type Registry struct {
	service string
	mu      sync.RWMutex
	entries map[string]*entry
}

// NewRegistry returns an empty registry. Every key registered in it must be
// named "<service>.…".
func NewRegistry(service string) *Registry {
	return &Registry{service: service, entries: map[string]*entry{}}
}

// Service returns the service the registry belongs to.
func (r *Registry) Service() string { return r.service }

// Register adds k to r and returns it for use with Get. It fails if the key is
// malformed, lacks bounds, has an unsupported type, or if its default does not
// satisfy its own bounds.
//
// Supported T: int64, float64, bool, string, []string, money.Money,
// time.Duration, and structs (stored as JSON, for tables such as the approval
// policy or a fee schedule).
func Register[T any](r *Registry, k Key[T]) (Key[T], error) {
	bad := func(format string, args ...any) (Key[T], error) {
		return k, fmt.Errorf("%w: %s: %s", ErrInvalidKey, k.Name, fmt.Sprintf(format, args...))
	}
	if !keyName.MatchString(k.Name) {
		return bad("name must look like service.setting")
	}
	if !strings.HasPrefix(k.Name, r.service+".") {
		return bad("name must start with %q", r.service+".")
	}
	if k.Validate == nil {
		return bad("Validate is required: a setting without bounds is not allowed")
	}
	if strings.TrimSpace(k.Description) == "" || strings.TrimSpace(k.Owner) == "" {
		return bad("Description and Owner are required")
	}
	if k.Scope != Global && k.Scope != Org {
		return bad("Scope must be Global or Org")
	}
	c, err := codecFor[T]()
	if err != nil {
		return bad("%v", err)
	}
	if err := k.Validate(k.Default); err != nil {
		return bad("default fails its own bounds: %v", err)
	}
	def, err := c.encode(k.Default)
	if err != nil {
		return bad("default: %v", err)
	}

	e := &entry{
		name: k.Name, scope: k.Scope, description: k.Description, owner: k.Owner, bounds: k.Bounds,
		typ: reflect.TypeFor[T](), typeName: c.name, defaultJSON: def,
		normalise: func(raw json.RawMessage) (json.RawMessage, error) {
			v, err := c.decode(raw)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidValue, err)
			}
			if err := k.Validate(v); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidValue, err)
			}
			return c.encode(v)
		},
		decode: func(raw json.RawMessage) (any, error) {
			v, err := c.decode(raw)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidValue, err)
			}
			if err := k.Validate(v); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidValue, err)
			}
			return v, nil
		},
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.entries[k.Name]; dup {
		return k, fmt.Errorf("%w: %s", ErrDuplicateKey, k.Name)
	}
	r.entries[k.Name] = e
	return k, nil
}

// MustRegister is Register for package-level key declarations; it panics on a
// definition error, which is a programming error found at start-up.
func MustRegister[T any](r *Registry, k Key[T]) Key[T] {
	k, err := Register(r, k)
	if err != nil {
		panic(err)
	}
	return k
}

func (r *Registry) lookup(name string) (*entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotRegistered, name)
	}
	return e, nil
}

// Names lists the registered keys, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.entries))
	for n := range r.entries {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// codec encodes and strictly decodes one supported value type.
type codec[T any] struct {
	name   string
	encode func(T) (json.RawMessage, error)
	decode func(json.RawMessage) (T, error)
}

func codecFor[T any]() (codec[T], error) {
	t := reflect.TypeFor[T]()
	switch t {
	case reflect.TypeFor[time.Duration]():
		return codec[T]{
			name: "duration",
			// A duration is a string such as "15m0s". A bare number would be
			// nanoseconds to Go and minutes to a person.
			encode: func(v T) (json.RawMessage, error) {
				return json.Marshal(reflect.ValueOf(v).Interface().(time.Duration).String())
			},
			decode: func(raw json.RawMessage) (T, error) {
				var zero T
				var s string
				if err := strictUnmarshal(raw, &s); err != nil {
					return zero, errors.New(`a duration must be a string such as "15m"`)
				}
				d, err := time.ParseDuration(s)
				if err != nil {
					return zero, err
				}
				return reflect.ValueOf(d).Interface().(T), nil
			},
		}, nil
	case reflect.TypeFor[money.Money]():
		return jsonCodec[T]("money", func(v T) error {
			return reflect.ValueOf(v).Interface().(money.Money).Validate()
		}), nil
	case reflect.TypeFor[int64](), reflect.TypeFor[bool](), reflect.TypeFor[string](), reflect.TypeFor[[]string]():
		return jsonCodec[T](t.String(), nil), nil
	case reflect.TypeFor[float64]():
		return jsonCodec[T]("float64", func(v T) error {
			f := reflect.ValueOf(v).Float()
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return errors.New("not a finite number")
			}
			return nil
		}), nil
	}
	if t.Kind() == reflect.Struct {
		return jsonCodec[T]("json", nil), nil
	}
	return codec[T]{}, fmt.Errorf("unsupported setting type %s (use int64, float64, bool, string, []string, money.Money, time.Duration or a struct)", t)
}

func jsonCodec[T any](name string, check func(T) error) codec[T] {
	return codec[T]{
		name: name,
		encode: func(v T) (json.RawMessage, error) {
			if check != nil {
				if err := check(v); err != nil {
					return nil, err
				}
			}
			return json.Marshal(v)
		},
		decode: func(raw json.RawMessage) (T, error) {
			var v T
			if err := strictUnmarshal(raw, &v); err != nil {
				return v, err
			}
			if check != nil {
				if err := check(v); err != nil {
					return v, err
				}
			}
			return v, nil
		},
	}
}

// strictUnmarshal rejects unknown fields and trailing data, so a typo in a
// proposed JSON struct is an error instead of a silently ignored field.
func strictUnmarshal(raw []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after value")
	}
	return nil
}

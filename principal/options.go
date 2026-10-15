package principal

// requirements are what a route demands of the caller.
type requirements struct {
	perms       []string
	minLevel    AuthLevel
	denyRO      bool
	realm       Realm
	skipMethods map[string]bool
}

// Option adds a requirement to Require or UnaryServerInterceptor.
type Option func(*requirements)

// WithPerm requires the permission. Repeated options must all hold.
func WithPerm(perm string) Option {
	return func(r *requirements) { r.perms = append(r.perms, perm) }
}

// WithAuthLevel requires at least the given level.
func WithAuthLevel(l AuthLevel) Option {
	return func(r *requirements) { r.minLevel = l }
}

// DenyReadOnly refuses read-only principals (including every impersonation).
// Put it on every route that changes state.
func DenyReadOnly() Option {
	return func(r *requirements) { r.denyRO = true }
}

// WithRealm requires the principal to be from the given realm.
func WithRealm(realm Realm) Option {
	return func(r *requirements) { r.realm = realm }
}

// SkipMethods exempts full gRPC method names (for example the health check)
// from the interceptor. It has no effect on the Echo middleware.
func SkipMethods(methods ...string) Option {
	return func(r *requirements) {
		if r.skipMethods == nil {
			r.skipMethods = map[string]bool{}
		}
		for _, m := range methods {
			r.skipMethods[m] = true
		}
	}
}

func newRequirements(opts []Option) *requirements {
	r := &requirements{}
	for _, o := range opts {
		o(r)
	}
	return r
}

// check applies the requirements in a fixed order, so the same caller gets the
// same refusal reason from every service.
func (r *requirements) check(p Principal) error {
	if r.realm != "" && p.Realm != r.realm {
		return ErrRealm
	}
	if p.AuthLevel < r.minLevel {
		return ErrAuthLevel
	}
	if r.denyRO && p.ReadOnly {
		return ErrReadOnly
	}
	for _, perm := range r.perms {
		if !p.HasPerm(perm) {
			return ErrPermission
		}
	}
	return nil
}

package settings

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Row is one stored version of a setting. Rows are never updated or deleted:
// history is how "which threshold applied to this payout on 3 March?" is answered.
type Row struct {
	Key string
	// OrgID is nil for the global value, and set for a per-organisation override.
	OrgID             *uuid.UUID
	Version           Version
	Value             json.RawMessage
	EffectiveFrom     time.Time
	ChangedBy         string
	ApprovalRequestID *uuid.UUID
	Reason            string
	CreatedAt         time.Time
}

// SeedActor is the ChangedBy of a row inserted from config at first boot.
const SeedActor = "seed"

// Store persists rows. The Postgres implementation is in package pgstore.
type Store interface {
	// Current returns, for every (key, organisation) pair, the row with the
	// highest version whose EffectiveFrom is not after now.
	Current(ctx context.Context, now time.Time) ([]Row, error)
	// Append stores row as the next version of (Key, OrgID) and returns it with
	// Version and CreatedAt assigned. Concurrent appends to one pair get
	// distinct, consecutive versions.
	Append(ctx context.Context, row Row) (Row, error)
	// Seed stores row as version 1 of (Key, OrgID) if, and only if, no row for
	// that pair exists. It reports whether it inserted one. It never replaces.
	Seed(ctx context.Context, row Row) (bool, error)
	// History returns every version of (key, org), newest first.
	History(ctx context.Context, key string, orgID *uuid.UUID) ([]Row, error)
}

// MemStore is an in-memory Store, for tests in this module and in services.
type MemStore struct {
	mu   sync.Mutex
	rows []Row
	now  func() time.Time
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore { return &MemStore{now: time.Now} }

func sameOrg(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (s *MemStore) Current(_ context.Context, now time.Time) ([]Row, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Row
	for _, r := range s.rows {
		if r.EffectiveFrom.After(now) {
			continue
		}
		replaced := false
		for i := range out {
			if out[i].Key == r.Key && sameOrg(out[i].OrgID, r.OrgID) {
				if r.Version > out[i].Version {
					out[i] = r
				}
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *MemStore) Append(_ context.Context, row Row) (Row, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row.Version = s.nextVersion(row.Key, row.OrgID)
	row.CreatedAt = s.now().UTC()
	s.rows = append(s.rows, row)
	return row, nil
}

func (s *MemStore) Seed(_ context.Context, row Row) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.Key == row.Key && sameOrg(r.OrgID, row.OrgID) {
			return false, nil
		}
	}
	row.Version = 1
	row.CreatedAt = s.now().UTC()
	s.rows = append(s.rows, row)
	return true, nil
}

func (s *MemStore) History(_ context.Context, key string, orgID *uuid.UUID) ([]Row, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Row
	for _, r := range s.rows {
		if r.Key == key && sameOrg(r.OrgID, orgID) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

func (s *MemStore) nextVersion(key string, orgID *uuid.UUID) Version {
	var max Version
	for _, r := range s.rows {
		if r.Key == key && sameOrg(r.OrgID, orgID) && r.Version > max {
			max = r.Version
		}
	}
	return max + 1
}

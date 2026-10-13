// Package pgstore is the Postgres implementation of settings.Store, over
// database/sql so it works with any driver (pgx's stdlib adapter, lib/pq).
//
// It reads and writes the table created by settings.MigrationUp.
package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"swiftbon.com/shared/settings"
)

// DBTX is what the store needs of a connection. *sql.DB, *sql.Conn and *sql.Tx
// all satisfy it.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type beginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// Store is a settings.Store over Postgres.
type Store struct{ db DBTX }

// New returns a Store over db. Pass a *sql.DB for normal use. Pass a *sql.Tx
// (with settings.UsingStore) to make Apply part of the caller's transaction, so
// the new version and the setting-changed outbox row commit together.
func New(db DBTX) *Store { return &Store{db: db} }

var _ settings.Store = (*Store)(nil)

const columns = `key, scope_org_id, version, value, effective_from, changed_by, approval_request_id, reason, created_at`

// orgArg converts an optional organisation to a query argument. It is passed as
// text and cast to uuid in the SQL, which works with every driver.
func orgArg(org *uuid.UUID) any {
	if org == nil {
		return nil
	}
	return org.String()
}

func (s *Store) Current(ctx context.Context, now time.Time) ([]settings.Row, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT ON (key, scope_org_id) `+columns+`
		FROM settings
		WHERE effective_from <= $1
		ORDER BY key, scope_org_id, version DESC`, now)
	if err != nil {
		return nil, err
	}
	return scanRows(rows)
}

func (s *Store) History(ctx context.Context, key string, orgID *uuid.UUID) ([]settings.Row, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+columns+`
		FROM settings
		WHERE key = $1 AND scope_org_id IS NOT DISTINCT FROM $2::uuid
		ORDER BY version DESC`, key, orgArg(orgID))
	if err != nil {
		return nil, err
	}
	return scanRows(rows)
}

// Append takes a transaction-scoped advisory lock on (key, organisation), so two
// concurrent appends cannot read the same maximum version. It opens its own
// transaction when the store holds a *sql.DB, and joins the caller's when it
// holds a *sql.Tx.
func (s *Store) Append(ctx context.Context, row settings.Row) (settings.Row, error) {
	db := s.db
	var tx *sql.Tx
	if b, ok := s.db.(beginner); ok {
		var err error
		if tx, err = b.BeginTx(ctx, nil); err != nil {
			return settings.Row{}, err
		}
		defer tx.Rollback() //nolint:errcheck // after Commit this is a no-op
		db = tx
	}

	if _, err := db.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':' || COALESCE($2::text, ''), 0))`,
		row.Key, orgArg(row.OrgID)); err != nil {
		return settings.Row{}, err
	}

	var approval any
	if row.ApprovalRequestID != nil {
		approval = row.ApprovalRequestID.String()
	}
	var version int64
	var created time.Time
	err := db.QueryRowContext(ctx, `
		INSERT INTO settings (key, scope_org_id, version, value, effective_from, changed_by, approval_request_id, reason)
		SELECT $1, $2::uuid,
		       COALESCE((SELECT MAX(version) FROM settings WHERE key = $1 AND scope_org_id IS NOT DISTINCT FROM $2::uuid), 0) + 1,
		       $3::jsonb, $4, $5, $6::uuid, $7
		RETURNING version, created_at`,
		row.Key, orgArg(row.OrgID), string(row.Value), row.EffectiveFrom, row.ChangedBy, approval, row.Reason,
	).Scan(&version, &created)
	if err != nil {
		return settings.Row{}, err
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return settings.Row{}, err
		}
	}
	row.Version, row.CreatedAt = settings.Version(version), created.UTC()
	return row, nil
}

// Seed inserts version 1 unless a row for the pair exists. ON CONFLICT DO
// NOTHING makes replicas booting together safe: one inserts, the rest do not.
func (s *Store) Seed(ctx context.Context, row settings.Row) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, scope_org_id, version, value, effective_from, changed_by, reason)
		SELECT $1, $2::uuid, 1, $3::jsonb, $4, $5, $6
		WHERE NOT EXISTS (SELECT 1 FROM settings WHERE key = $1 AND scope_org_id IS NOT DISTINCT FROM $2::uuid)
		ON CONFLICT DO NOTHING`,
		row.Key, orgArg(row.OrgID), string(row.Value), row.EffectiveFrom, row.ChangedBy, row.Reason)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func scanRows(rows *sql.Rows) ([]settings.Row, error) {
	defer rows.Close()
	var out []settings.Row
	for rows.Next() {
		var (
			r        settings.Row
			org      sql.NullString
			approval sql.NullString
			value    []byte
			version  int64
		)
		if err := rows.Scan(&r.Key, &org, &version, &value, &r.EffectiveFrom, &r.ChangedBy, &approval, &r.Reason, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Version, r.Value = settings.Version(version), json.RawMessage(value)
		r.EffectiveFrom, r.CreatedAt = r.EffectiveFrom.UTC(), r.CreatedAt.UTC()
		if org.Valid {
			id, err := uuid.Parse(org.String)
			if err != nil {
				return nil, fmt.Errorf("pgstore: scope_org_id %q: %w", org.String, err)
			}
			r.OrgID = &id
		}
		if approval.Valid {
			id, err := uuid.Parse(approval.String)
			if err != nil {
				return nil, fmt.Errorf("pgstore: approval_request_id %q: %w", approval.String, err)
			}
			r.ApprovalRequestID = &id
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

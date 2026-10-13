package pgstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"

	"swiftbon.com/shared/settings"
	"swiftbon.com/shared/settings/pgstore"
)

// These tests need a real Postgres (15 or later is not required; 12+ is enough).
// They run only when SETTINGS_TEST_DSN is set, for example:
//
//	SETTINGS_TEST_DSN='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' go test ./settings/pgstore
//
// Each test creates and drops its own schema, so the database can be shared.
func openDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("SETTINGS_TEST_DSN")
	if dsn == "" {
		t.Skip("SETTINGS_TEST_DSN not set; skipping Postgres integration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	schema := "settings_test_" + uuid.NewString()[:8]
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	// Pin every pooled connection to the schema.
	db.Close()
	db, err = sql.Open("pgx", dsn+"&search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() {
		db.Close()
		admin, err := sql.Open("pgx", dsn)
		if err == nil {
			_, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE")
			admin.Close()
		}
	})
	if _, err := db.ExecContext(ctx, settings.MigrationUp); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	// Up, down, up.
	if _, err := db.ExecContext(ctx, settings.MigrationDown); err != nil {
		t.Fatalf("migration down: %v", err)
	}
	if _, err := db.ExecContext(ctx, settings.MigrationUp); err != nil {
		t.Fatalf("migration up again: %v", err)
	}
	return db
}

func row(key string, org *uuid.UUID, value string) settings.Row {
	return settings.Row{Key: key, OrgID: org, Value: json.RawMessage(value), EffectiveFrom: time.Now().Add(-time.Minute).UTC(), ChangedBy: "maker", Reason: "test"}
}

func TestAppendCurrentHistory(t *testing.T) {
	st := pgstore.New(openDB(t))
	ctx := context.Background()
	org := uuid.New()
	approval := uuid.New()

	g1, err := st.Append(ctx, row("payouts.a", nil, `1`))
	if err != nil || g1.Version != 1 {
		t.Fatalf("g1 = %+v, %v", g1, err)
	}
	r2 := row("payouts.a", nil, `2`)
	r2.ApprovalRequestID = &approval
	g2, err := st.Append(ctx, r2)
	if err != nil || g2.Version != 2 {
		t.Fatalf("g2 = %+v, %v", g2, err)
	}
	o1, err := st.Append(ctx, row("payouts.a", &org, `9`))
	if err != nil || o1.Version != 1 {
		t.Fatalf("o1 = %+v, %v", o1, err)
	}
	future := row("payouts.a", nil, `3`)
	future.EffectiveFrom = time.Now().Add(time.Hour).UTC()
	if _, err := st.Append(ctx, future); err != nil {
		t.Fatal(err)
	}

	cur, err := st.Current(ctx, time.Now())
	if err != nil || len(cur) != 2 {
		t.Fatalf("current = %+v, %v", cur, err)
	}
	for _, r := range cur {
		switch {
		case r.OrgID == nil && (r.Version != 2 || string(r.Value) != "2" || r.ApprovalRequestID == nil || *r.ApprovalRequestID != approval):
			t.Errorf("global current = %+v", r)
		case r.OrgID != nil && (*r.OrgID != org || string(r.Value) != "9"):
			t.Errorf("org current = %+v", r)
		}
	}

	h, err := st.History(ctx, "payouts.a", nil)
	if err != nil || len(h) != 3 || h[0].Version != 3 || h[2].Version != 1 {
		t.Errorf("history = %+v, %v", h, err)
	}
	h, _ = st.History(ctx, "payouts.a", &org)
	if len(h) != 1 {
		t.Errorf("org history = %+v", h)
	}
}

func TestSeedNeverOverwrites(t *testing.T) {
	st := pgstore.New(openDB(t))
	ctx := context.Background()
	ok, err := st.Seed(ctx, row("payouts.s", nil, `1`))
	if err != nil || !ok {
		t.Fatalf("first seed = %v, %v", ok, err)
	}
	ok, err = st.Seed(ctx, row("payouts.s", nil, `2`))
	if err != nil || ok {
		t.Fatalf("second seed = %v, %v", ok, err)
	}
	h, _ := st.History(ctx, "payouts.s", nil)
	if len(h) != 1 || string(h[0].Value) != "1" {
		t.Errorf("history = %+v", h)
	}
}

func TestConcurrentAppendsGetDistinctVersions(t *testing.T) {
	st := pgstore.New(openDB(t))
	ctx := context.Background()
	const n = 12
	var wg sync.WaitGroup
	versions := make(chan settings.Version, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := st.Append(ctx, row("payouts.c", nil, `1`))
			if err != nil {
				t.Error(err)
				return
			}
			versions <- r.Version
		}()
	}
	wg.Wait()
	close(versions)
	seen := map[settings.Version]bool{}
	for v := range versions {
		if seen[v] {
			t.Errorf("version %d assigned twice", v)
		}
		seen[v] = true
	}
	if len(seen) != n {
		t.Errorf("got %d distinct versions, want %d", len(seen), n)
	}
}

func TestAppendInCallersTransaction(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pgstore.New(tx).Append(ctx, row("payouts.t", nil, `1`)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	h, _ := pgstore.New(db).History(ctx, "payouts.t", nil)
	if len(h) != 0 {
		t.Errorf("rolled-back append left %d rows", len(h))
	}
}

func TestRowsAreImmutable(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	if _, err := pgstore.New(db).Append(ctx, row("payouts.i", nil, `1`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE settings SET value = '2'::jsonb`); err == nil {
		t.Error("UPDATE was allowed")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM settings`); err == nil {
		t.Error("DELETE was allowed")
	}
}

func TestGlobalVersionsAreUnique(t *testing.T) {
	// Two global rows with the same version must be refused even though
	// scope_org_id is NULL.
	db := openDB(t)
	ctx := context.Background()
	ins := `INSERT INTO settings (key, version, value, effective_from, changed_by, reason) VALUES ('payouts.u', 1, '1', now(), 'x', 'x')`
	if _, err := db.ExecContext(ctx, ins); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, ins); err == nil {
		t.Error("duplicate global version accepted")
	}
}

func TestManagerEndToEnd(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	reg := settings.NewRegistry("payouts")
	k := settings.MustRegister(reg, settings.Key[int64]{Name: "payouts.n", Default: 3, Scope: settings.Org,
		Validate: func(v int64) error { return nil }, Description: "d", Owner: "o"})
	m := settings.NewManager(reg, pgstore.New(db))
	if n, err := m.Seed(ctx); err != nil || n != 1 {
		t.Fatalf("seed = %d, %v", n, err)
	}
	org := uuid.New()
	apr := uuid.New()
	if _, _, err := m.Apply(ctx, settings.Change{Key: "payouts.n", OrgID: &org, Value: json.RawMessage(`8`), Reason: "r", ChangedBy: "m", ApprovalRequestID: &apr}); err != nil {
		t.Fatal(err)
	}
	if v, ver, err := settings.Get(ctx, m, k, &org); err != nil || v != 8 || ver != 1 {
		t.Errorf("org = %d v%d, %v", v, ver, err)
	}
	if v, ver, err := settings.Get(ctx, m, k, nil); err != nil || v != 3 || ver != 1 {
		t.Errorf("global = %d v%d, %v", v, ver, err)
	}
}

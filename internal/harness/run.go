package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Itz-Agasta/mend/demo/shop"
)

// Scratch database names. Each harness run recreates them.
const (
	dbClean    = "mend_h_clean"
	dbAttacked = "mend_h_attacked"
	dbPITR     = "mend_h_pitr"
	dbRevert   = "mend_h_revert"
)

// Run executes the scenario under every recovery strategy and compares each
// result with the clean run, in which the bad action never happened.
func Run(ctx context.Context, pgURL string, s Scenario) (*Report, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	c, err := openCluster(ctx, pgURL)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	clean, _, err := execute(ctx, c, s, dbClean, s.Ops, false)
	if err != nil {
		return nil, fmt.Errorf("clean run: %w", err)
	}
	attacked, badEffect, err := execute(ctx, c, s, dbAttacked, s.Ops, true)
	if err != nil {
		return nil, fmt.Errorf("attacked run: %w", err)
	}
	if badEffect.Empty() {
		return nil, fmt.Errorf("scenario %s: the bad action changed nothing", s.Name)
	}
	pitr, _, err := execute(ctx, c, s, dbPITR, s.BadAt, false)
	if err != nil {
		return nil, fmt.Errorf("point-in-time run: %w", err)
	}
	reverted, err := revertOnly(ctx, c, badEffect)
	if err != nil {
		return nil, fmt.Errorf("revert-only run: %w", err)
	}

	r := &Report{
		Scenario:  s.Name,
		Ops:       s.Ops,
		BadAt:     s.BadAt,
		BadActor:  s.BadActor,
		BadSQL:    s.BadSQL,
		BadEffect: summarize(badEffect),
	}
	r.add("no repair", "keep the attacked database as it is", clean, attacked)
	r.add("point-in-time restore", "restore to just before the bad action; everything after it is lost", clean, pitr)
	r.add("revert bad action only", "restore only the cells the bad action changed (an ideal row-level undo)", clean, reverted)
	return r, nil
}

// execute runs ops[:until] against a freshly seeded shop in database name. If
// injectBad is set, the bad action runs just before op BadAt, and its effect
// is returned as a diff from the state before it to the state after.
func execute(ctx context.Context, c *cluster, s Scenario, name string, until int, injectBad bool) (Snapshot, Diff, error) {
	if err := c.fresh(ctx, name); err != nil {
		return nil, nil, err
	}
	db, err := c.open(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()
	if err := shop.Setup(ctx, db, s.Catalog()); err != nil {
		return nil, nil, err
	}

	srv := httptest.NewServer(shop.New(db, shop.WithRequestClock()).Handler())
	defer srv.Close()

	ops := s.Workload()[:until]
	split := min(s.BadAt, until)
	if err := replay(ctx, srv.URL, ops[:split]); err != nil {
		return nil, nil, err
	}
	var effect Diff
	if injectBad && until >= s.BadAt {
		// The bad action arrives on its own connection, as an agent's SQL
		// tool would issue it, not through the shop's API.
		effect, err = applyBad(ctx, db, s.BadSQL)
		if err != nil {
			return nil, nil, err
		}
	}
	if err := replay(ctx, srv.URL, ops[split:]); err != nil {
		return nil, nil, err
	}
	snap, err := TakeSnapshot(ctx, db)
	return snap, effect, err
}

func applyBad(ctx context.Context, db *pgxpool.Pool, sql string) (Diff, error) {
	before, err := TakeSnapshot(ctx, db)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(ctx, sql); err != nil {
		return nil, fmt.Errorf("bad action: %w", err)
	}
	after, err := TakeSnapshot(ctx, db)
	if err != nil {
		return nil, err
	}
	return Compare(before, after), nil
}

// revertOnly copies the attacked database and puts back exactly the cells the
// bad action changed, leaving every later write in place. This is the best a
// pure undo tool can do without re-executing dependent work.
func revertOnly(ctx context.Context, c *cluster, badEffect Diff) (Snapshot, error) {
	if err := c.clone(ctx, dbAttacked, dbRevert); err != nil {
		return nil, err
	}
	db, err := c.open(ctx, dbRevert)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	for _, td := range badEffect {
		if err := undoTable(ctx, tx, td); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return TakeSnapshot(ctx, db)
}

// undoTable reverses one table's part of a bad action's effect, where want is
// the state before the action and got the state after it.
func undoTable(ctx context.Context, tx pgx.Tx, td TableDiff) error {
	table := pgx.Identifier{td.Table}.Sanitize()
	for _, pk := range td.Extra {
		if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE id::text = $1`, pk); err != nil {
			return fmt.Errorf("undo insert into %s: %w", td.Table, err)
		}
	}
	for _, ch := range td.Changed {
		before := map[string]any{}
		var set []byte
		for col, vals := range ch.Columns {
			before[col] = vals[0]
			if len(set) > 0 {
				set = append(set, ", "...)
			}
			ident := pgx.Identifier{col}.Sanitize()
			set = fmt.Appendf(set, "%s = r.%s", ident, ident)
		}
		js, err := json.Marshal(before)
		if err != nil {
			return err
		}
		q := fmt.Sprintf(`UPDATE %s AS t SET %s FROM json_populate_record(NULL::%s, $1::json) AS r WHERE t.id::text = $2`,
			table, set, table)
		if _, err := tx.Exec(ctx, q, js, ch.PK); err != nil {
			return fmt.Errorf("undo update of %s %s: %w", td.Table, ch.PK, err)
		}
	}
	if len(td.Missing) > 0 {
		// Restoring deleted rows needs their before-images, which the diff
		// does not carry. No scenario deletes rows yet.
		return fmt.Errorf("undo delete from %s: not supported yet", td.Table)
	}
	return nil
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// replay sends each op to the shop at baseURL and checks its status.
func replay(ctx context.Context, baseURL string, ops []Op) error {
	for i, op := range ops {
		var (
			method, path string
			body         any
			want         int
		)
		switch op.Kind {
		case OpOrder:
			method, path, want = http.MethodPost, "/orders", http.StatusCreated
			body = map[string]any{"customer": op.Customer, "product_id": op.ProductID, "qty": op.Qty}
		case OpRestock:
			method, path, want = http.MethodPost, fmt.Sprintf("/products/%d/restock", op.ProductID), http.StatusNoContent
			body = map[string]any{"qty": op.Qty}
		case OpView:
			method, path, want = http.MethodGet, fmt.Sprintf("/products/%d", op.ProductID), http.StatusOK
		}
		var rd io.Reader
		if body != nil {
			js, err := json.Marshal(body)
			if err != nil {
				return err
			}
			rd = bytes.NewReader(js)
		}
		req, err := http.NewRequestWithContext(ctx, method, baseURL+path, rd)
		if err != nil {
			return err
		}
		req.Header.Set(shop.TimeHeader, op.At.Format(time.RFC3339))
		resp, err := httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("op %d (%s): %w", i, op.Kind, err)
		}
		msg, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != want {
			return fmt.Errorf("op %d (%s): status %d, want %d: %s", i, op.Kind, resp.StatusCode, want, bytes.TrimSpace(msg))
		}
	}
	return nil
}

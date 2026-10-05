package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tables are the demo shop's tables; each has an "id" primary key.
var Tables = []string{"products", "orders"}

// Row is one row as decoded from row_to_json. Numbers stay json.Number so
// comparisons are exact.
type Row map[string]any

// Snapshot is the full contents of a database: table → primary key → row.
type Snapshot map[string]map[string]Row

func TakeSnapshot(ctx context.Context, db *pgxpool.Pool) (Snapshot, error) {
	snap := Snapshot{}
	for _, table := range Tables {
		q := fmt.Sprintf(`SELECT id::text, row_to_json(t)::text FROM %s t`, pgx.Identifier{table}.Sanitize())
		rows, err := db.Query(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", table, err)
		}
		byPK := map[string]Row{}
		var pk, raw string
		_, err = pgx.ForEachRow(rows, []any{&pk, &raw}, func() error {
			dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
			dec.UseNumber()
			var row Row
			if err := dec.Decode(&row); err != nil {
				return err
			}
			byPK[pk] = row
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", table, err)
		}
		snap[table] = byPK
	}
	return snap, nil
}

// TableDiff is how one table in got differs from the same table in want.
type TableDiff struct {
	Table   string      `json:"table"`
	Missing []string    `json:"missing"` // primary keys in want but not in got
	Extra   []string    `json:"extra"`   // primary keys in got but not in want
	Changed []RowChange `json:"changed"`
}

type RowChange struct {
	PK      string            `json:"pk"`
	Columns map[string][2]any `json:"columns"` // column → [want, got]
}

type Diff []TableDiff

// Compare reports how got differs from want, table by table, with primary
// keys sorted for stable output.
func Compare(want, got Snapshot) Diff {
	var diff Diff
	for _, table := range Tables {
		w, g := want[table], got[table]
		td := TableDiff{Table: table}
		for pk, wr := range w {
			gr, ok := g[pk]
			if !ok {
				td.Missing = append(td.Missing, pk)
				continue
			}
			if cols := changedColumns(wr, gr); len(cols) > 0 {
				td.Changed = append(td.Changed, RowChange{PK: pk, Columns: cols})
			}
		}
		for pk := range g {
			if _, ok := w[pk]; !ok {
				td.Extra = append(td.Extra, pk)
			}
		}
		slices.SortFunc(td.Missing, comparePK)
		slices.SortFunc(td.Extra, comparePK)
		slices.SortFunc(td.Changed, func(a, b RowChange) int { return comparePK(a.PK, b.PK) })
		diff = append(diff, td)
	}
	return diff
}

func changedColumns(want, got Row) map[string][2]any {
	cols := map[string][2]any{}
	for col, wv := range want {
		if gv := got[col]; !reflect.DeepEqual(wv, gv) {
			cols[col] = [2]any{wv, gv}
		}
	}
	for col, gv := range got {
		if _, ok := want[col]; !ok {
			cols[col] = [2]any{nil, gv}
		}
	}
	return cols
}

// comparePK orders numeric primary keys numerically (shorter first), falling
// back to string order for equal lengths.
func comparePK(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (d Diff) Empty() bool {
	for _, td := range d {
		if len(td.Missing)+len(td.Extra)+len(td.Changed) > 0 {
			return false
		}
	}
	return true
}

// Table returns the diff for one table, or an empty one.
func (d Diff) Table(name string) TableDiff {
	for _, td := range d {
		if td.Table == name {
			return td
		}
	}
	return TableDiff{Table: name}
}

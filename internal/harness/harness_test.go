package harness

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestWorkloadIsDeterministic(t *testing.T) {
	s := PricingBot()
	if !reflect.DeepEqual(s.Workload(), s.Workload()) {
		t.Fatal("same seed produced different workloads")
	}
	if !reflect.DeepEqual(s.Catalog(), s.Catalog()) {
		t.Fatal("same seed produced different catalogs")
	}
	short := s
	short.Ops = 10
	if !reflect.DeepEqual(short.Catalog(), s.Catalog()) {
		t.Fatal("workload length changed the catalog")
	}
}

func TestValidate(t *testing.T) {
	s := PricingBot()
	s.BadAt = s.Ops + 1
	if s.Validate() == nil {
		t.Fatal("bad action past the end of the workload was accepted")
	}
}

func row(t *testing.T, js string) Row {
	t.Helper()
	var r Row
	dec := json.NewDecoder(strings.NewReader(js))
	dec.UseNumber()
	if err := dec.Decode(&r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCompare(t *testing.T) {
	want := Snapshot{
		"products": {"1": row(t, `{"id":1,"price_cents":1000}`), "2": row(t, `{"id":2,"price_cents":500}`)},
		"orders":   {"10": row(t, `{"id":10,"total_cents":1000}`)},
	}
	got := Snapshot{
		"products": {"1": row(t, `{"id":1,"price_cents":100}`), "2": row(t, `{"id":2,"price_cents":500}`)},
		"orders":   {"11": row(t, `{"id":11,"total_cents":1000}`)},
	}
	d := Compare(want, got)
	if d.Empty() {
		t.Fatal("diff is empty")
	}
	p := d.Table("products")
	if len(p.Changed) != 1 || p.Changed[0].PK != "1" || len(p.Missing)+len(p.Extra) != 0 {
		t.Fatalf("products diff = %+v", p)
	}
	if c := p.Changed[0].Columns["price_cents"]; c[0] != json.Number("1000") || c[1] != json.Number("100") {
		t.Fatalf("price change = %v", c)
	}
	o := d.Table("orders")
	if !reflect.DeepEqual(o.Missing, []string{"10"}) || !reflect.DeepEqual(o.Extra, []string{"11"}) {
		t.Fatalf("orders diff = %+v", o)
	}
	if !Compare(want, want).Empty() {
		t.Fatal("snapshot differs from itself")
	}
}

func TestComparePKOrdersNumerically(t *testing.T) {
	if comparePK("9", "10") >= 0 || comparePK("10", "9") <= 0 || comparePK("10", "10") != 0 {
		t.Fatal("primary keys are not ordered numerically")
	}
}

// TestRun needs a Postgres server; set MEND_PG_URL (see the Makefile).
func TestRun(t *testing.T) {
	url := os.Getenv("MEND_PG_URL")
	if url == "" {
		t.Skip("MEND_PG_URL not set")
	}
	s := PricingBot()
	s.Ops, s.BadAt = 300, 100
	r, err := Run(context.Background(), url, s)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Outcome{}
	for _, o := range r.Strategies {
		byName[o.Name] = o
	}
	if byName["no repair"].VsClean["products"].Wrong == 0 {
		t.Error("the attacked run matches the clean one")
	}
	if byName["point-in-time restore"].VsClean["orders"].Missing == 0 {
		t.Error("point-in-time restore lost no orders")
	}
	revert := byName["revert bad action only"].VsClean
	if revert["products"].Total() != 0 {
		t.Errorf("revert-only left products wrong: %+v", revert["products"])
	}
	if revert["orders"].Wrong == 0 {
		t.Error("revert-only left no orders wrong; the scenario has no dependent work")
	}
}

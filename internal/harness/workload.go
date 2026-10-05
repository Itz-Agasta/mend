// Package harness runs reproducible incidents against the demo shop and
// measures each recovery strategy against ground truth: the same workload in
// a world where the bad action never happened.
package harness

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/Itz-Agasta/mend/demo/shop"
)

// Scenario is one reproducible incident: a seeded workload with a bad action
// injected just before op BadAt.
type Scenario struct {
	Name     string
	Seed     uint64
	Products int
	Ops      int
	BadAt    int
	BadActor string
	BadSQL   string
	Start    time.Time
}

// PricingBot is the headline scenario: an agent asked for "10% off garden
// products" divides their prices by ten, and orders keep coming in.
func PricingBot() Scenario {
	return Scenario{
		Name:     "pricing-bot",
		Seed:     42,
		Products: 200,
		Ops:      4000,
		BadAt:    1000,
		BadActor: "agent:pricing-bot",
		BadSQL:   `UPDATE products SET price_cents = price_cents / 10 WHERE category = 'garden'`,
		Start:    time.Date(2026, 4, 25, 9, 0, 0, 0, time.UTC),
	}
}

func (s Scenario) Validate() error {
	if s.Products <= 0 || s.Ops <= 0 {
		return fmt.Errorf("scenario %s: products and ops must be positive", s.Name)
	}
	if s.BadAt < 0 || s.BadAt > s.Ops {
		return fmt.Errorf("scenario %s: bad action at op %d is outside 0..%d", s.Name, s.BadAt, s.Ops)
	}
	return nil
}

var categories = []string{"audio", "books", "garden", "toys"}

// Catalog returns the scenario's products. It uses its own random stream, so
// changing the workload length does not change the catalog.
func (s Scenario) Catalog() []shop.Product {
	r := rand.New(rand.NewPCG(s.Seed, 1))
	out := make([]shop.Product, s.Products)
	for i := range out {
		out[i] = shop.Product{
			ID:         int64(i + 1),
			SKU:        fmt.Sprintf("SKU-%04d", i+1),
			Category:   categories[i%len(categories)],
			PriceCents: 500 + r.Int64N(19_500),
			// Stock is high enough that no order is ever refused, keeping the
			// ground truth about prices rather than stock-outs.
			Stock: 1_000_000,
		}
	}
	return out
}

type OpKind int

const (
	OpOrder OpKind = iota
	OpRestock
	OpView
)

func (k OpKind) String() string {
	return [...]string{"order", "restock", "view"}[k]
}

// Op is one client request in the workload.
type Op struct {
	Kind      OpKind
	At        time.Time
	Customer  string
	ProductID int64
	Qty       int64
}

// Workload returns the scenario's requests in order: 70% orders, 10%
// restocks, 20% product views, one second apart.
func (s Scenario) Workload() []Op {
	r := rand.New(rand.NewPCG(s.Seed, 2))
	ops := make([]Op, s.Ops)
	for i := range ops {
		op := Op{
			At:        s.Start.Add(time.Duration(i) * time.Second),
			ProductID: 1 + r.Int64N(int64(s.Products)),
		}
		switch p := r.IntN(100); {
		case p < 70:
			op.Kind = OpOrder
			op.Customer = fmt.Sprintf("cust-%03d", r.IntN(500))
			op.Qty = 1 + r.Int64N(8)
		case p < 80:
			op.Kind = OpRestock
			op.Qty = 10 + r.Int64N(90)
		default:
			op.Kind = OpView
		}
		ops[i] = op
	}
	return ops
}

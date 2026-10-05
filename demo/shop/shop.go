// Package shop is a deliberately small web shop used as mend's demo and test
// workload. Orders read a product's price and compute their total in Go, so a
// bad price leaks into later orders through application logic.
package shop

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

// TimeHeader carries the request time when the server uses WithRequestClock.
const TimeHeader = "X-Shop-Time"

type Product struct {
	ID         int64  `json:"id"`
	SKU        string `json:"sku"`
	Category   string `json:"category"`
	PriceCents int64  `json:"price_cents"`
	Stock      int64  `json:"stock"`
}

type Order struct {
	ID             int64     `json:"id"`
	Customer       string    `json:"customer"`
	ProductID      int64     `json:"product_id"`
	Qty            int64     `json:"qty"`
	UnitPriceCents int64     `json:"unit_price_cents"`
	TotalCents     int64     `json:"total_cents"`
	CreatedAt      time.Time `json:"created_at"`
}

// Setup creates the shop's tables in an empty database and loads the catalog.
func Setup(ctx context.Context, db *pgxpool.Pool, catalog []Product) error {
	if _, err := db.Exec(ctx, schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	rows := make([][]any, len(catalog))
	for i, p := range catalog {
		rows[i] = []any{p.ID, p.SKU, p.Category, p.PriceCents, p.Stock}
	}
	_, err := db.CopyFrom(ctx, pgx.Identifier{"products"},
		[]string{"id", "sku", "category", "price_cents", "stock"}, pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("load catalog: %w", err)
	}
	return nil
}

// Server serves the shop's HTTP API.
type Server struct {
	db  *pgxpool.Pool
	now func(*http.Request) (time.Time, error)
}

type Option func(*Server)

// WithRequestClock makes the server take the current time from the
// X-Shop-Time header (RFC 3339) instead of the wall clock, so two runs of the
// same workload produce identical rows.
func WithRequestClock() Option {
	return func(s *Server) {
		s.now = func(r *http.Request) (time.Time, error) {
			return time.Parse(time.RFC3339, r.Header.Get(TimeHeader))
		}
	}
}

func New(db *pgxpool.Pool, opts ...Option) *Server {
	s := &Server{
		db:  db,
		now: func(*http.Request) (time.Time, error) { return time.Now(), nil },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /products/{id}", s.getProduct)
	mux.HandleFunc("POST /products/{id}/restock", s.restock)
	mux.HandleFunc("POST /orders", s.createOrder)
	return mux
}

func (s *Server) getProduct(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad product id", http.StatusBadRequest)
		return
	}
	var p Product
	err = s.db.QueryRow(r.Context(),
		`SELECT id, sku, category, price_cents, stock FROM products WHERE id = $1`, id).
		Scan(&p.ID, &p.SKU, &p.Category, &p.PriceCents, &p.Stock)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "no such product", http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) restock(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad product id", http.StatusBadRequest)
		return
	}
	var req struct {
		Qty int64 `json:"qty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Qty <= 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	tag, err := s.db.Exec(r.Context(),
		`UPDATE products SET stock = stock + $1 WHERE id = $2`, req.Qty, id)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "no such product", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Customer  string `json:"customer"`
		ProductID int64  `json:"product_id"`
		Qty       int64  `json:"qty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Customer == "" || req.Qty <= 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	now, err := s.now(r)
	if err != nil {
		http.Error(w, "bad request time", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var price, stock int64
	err = tx.QueryRow(ctx,
		`SELECT price_cents, stock FROM products WHERE id = $1 FOR UPDATE`, req.ProductID).
		Scan(&price, &stock)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "no such product", http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if stock < req.Qty {
		http.Error(w, "out of stock", http.StatusConflict)
		return
	}

	o := Order{
		Customer:       req.Customer,
		ProductID:      req.ProductID,
		Qty:            req.Qty,
		UnitPriceCents: price,
		TotalCents:     orderTotal(price, req.Qty),
		CreatedAt:      now,
	}
	if _, err := tx.Exec(ctx,
		`UPDATE products SET stock = stock - $1 WHERE id = $2`, req.Qty, req.ProductID); err != nil {
		serverError(w, err)
		return
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO orders (customer, product_id, qty, unit_price_cents, total_cents, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		o.Customer, o.ProductID, o.Qty, o.UnitPriceCents, o.TotalCents, o.CreatedAt).Scan(&o.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, o)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // client went away
}

func serverError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

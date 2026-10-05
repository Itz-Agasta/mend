package harness

import (
	"context"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// cluster creates and clones the scratch databases each run uses. Admin
// statements go through the database named in the base URL (usually
// "postgres").
type cluster struct {
	admin   *pgxpool.Pool
	baseURL *url.URL
}

func openCluster(ctx context.Context, rawURL string) (*cluster, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse postgres url: %w", err)
	}
	admin, err := pgxpool.New(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		return nil, fmt.Errorf("connect to %s: %w", u.Redacted(), err)
	}
	return &cluster{admin: admin, baseURL: u}, nil
}

func (c *cluster) Close() { c.admin.Close() }

// fresh drops and recreates an empty database.
func (c *cluster) fresh(ctx context.Context, name string) error {
	return c.recreate(ctx, name, "")
}

// clone replaces dst with a copy of src. Nothing may be connected to src.
func (c *cluster) clone(ctx context.Context, src, dst string) error {
	return c.recreate(ctx, dst, src)
}

func (c *cluster) recreate(ctx context.Context, name, template string) error {
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := c.admin.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("drop %s: %w", name, err)
	}
	create := "CREATE DATABASE " + ident
	if template != "" {
		create += " TEMPLATE " + pgx.Identifier{template}.Sanitize()
	}
	if _, err := c.admin.Exec(ctx, create); err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	return nil
}

func (c *cluster) open(ctx context.Context, name string) (*pgxpool.Pool, error) {
	u := *c.baseURL
	u.Path = "/" + name
	return pgxpool.New(ctx, u.String())
}

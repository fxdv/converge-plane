// Package db manages the PostgreSQL connection pool.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps the connection pool. Services and repositories receive this
// value and must scope every query to the caller's tenant.
type DB struct {
	Pool *pgxpool.Pool
}

// New opens a pool and verifies connectivity. It fails fast so that a bad
// DSN or unreachable database aborts startup instead of serving a broken
// instance.
func New(ctx context.Context, databaseURL string, minConns, maxConns int32) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MinConns = minConns
	cfg.MaxConns = maxConns
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &DB{Pool: pool}, nil
}

// Healthy reports whether the database answers within a short deadline.
// Used by the readiness probe; it is intentionally stricter than liveness.
func (d *DB) Healthy(ctx context.Context) error {
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return d.Pool.Ping(c)
}

// Close releases the pool.
func (d *DB) Close() {
	d.Pool.Close()
}

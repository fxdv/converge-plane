// Package db manages the PostgreSQL connection pool.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"converge/internal/metrics"
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

// CollectMetrics reports the pool's state at scrape time: saturation
// (acquired against max) and time spent waiting for a connection are the
// early signs of an undersized pool or a stuck transaction.
func (d *DB) CollectMetrics(e *metrics.Emitter) {
	s := d.Pool.Stat()
	const conns = "Pool connections by state."
	e.Gauge("converge_db_pool_connections", conns, float64(s.AcquiredConns()), "state", "acquired")
	e.Gauge("converge_db_pool_connections", conns, float64(s.IdleConns()), "state", "idle")
	e.Gauge("converge_db_pool_connections", conns, float64(s.ConstructingConns()), "state", "constructing")
	e.Gauge("converge_db_pool_max_connections", "Pool size limit.", float64(s.MaxConns()))
	e.Counter("converge_db_pool_acquires_total", "Connections acquired from the pool.", float64(s.AcquireCount()))
	e.Counter("converge_db_pool_empty_acquires_total", "Acquires that had to wait for a connection.", float64(s.EmptyAcquireCount()))
	e.Counter("converge_db_pool_acquire_seconds_total", "Time spent acquiring connections.", s.AcquireDuration().Seconds())
}

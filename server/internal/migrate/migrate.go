// Package migrate applies embedded SQL migrations to the database.
//
// Migrations are numbered files (NNNN_name.sql) embedded at build time and
// applied in ascending order inside a single transaction, recorded in
// schema_migrations. The server applies migrations on startup by default
// (CIRCLE_AUTO_Migrate=true); operators can disable this and apply them in
// a controlled window instead.
package migrate

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var fs embed.FS

// Migration is a single numbered SQL file.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// load reads and orders all embedded migrations.
func load() ([]Migration, error) {
	entries, err := fs.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var migs []Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		parts := strings.SplitN(e.Name(), "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("malformed migration file name %q: expected NNNN_name.sql", e.Name())
		}
		v, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("malformed migration version in %q: %w", e.Name(), err)
		}
		data, err := fs.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", e.Name(), err)
		}
		migs = append(migs, Migration{Version: v, Name: parts[1], SQL: string(data)})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].Version < migs[j].Version })
	return migs, nil
}

const ensureTable = `
create table if not exists schema_migrations (
  version integer primary key,
  name text not null,
  applied_at timestamptz not null default now()
);`

// Run applies all unapplied migrations in one transaction.
func Run(ctx context.Context, pool *pgxpool.Pool) error {
	migs, err := load()
	if err != nil {
		return err
	}
	if len(migs) == 0 {
		return fmt.Errorf("no migrations found")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, ensureTable); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	var v int
	done := map[int]bool{}
	rows, err := tx.Query(ctx, "select version from schema_migrations")
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for rows.Next() {
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("scan schema_migrations: %w", err)
		}
		done[v] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate schema_migrations: %w", err)
	}
	rows.Close()

	for _, m := range migs {
		if done[m.Version] {
			continue
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			return fmt.Errorf("apply migration %04d_%s: %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx,
			"insert into schema_migrations (version, name) values ($1, $2)",
			m.Version, m.Name,
		); err != nil {
			return fmt.Errorf("record migration %d: %w", m.Version, err)
		}
	}

	return tx.Commit(ctx)
}

// Latest reports the highest applied version (0 when none).
func Latest(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var v int
	err := pool.QueryRow(ctx, "select coalesce(max(version), 0) from schema_migrations").Scan(&v)
	return v, err
}

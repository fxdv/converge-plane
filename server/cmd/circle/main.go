// Command circle runs the Circle API server.
//
// Circle is a fast, self-hostable issue tracker for software teams.
// The binary starts PostgreSQL-backed services, applies schema migrations,
// and serves the JSON API (plus SSE streams in later milestones).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"circle/internal/config"
	"circle/internal/db"
	"circle/internal/httpx"
	"circle/internal/logging"
	"circle/internal/migrate"
)

// Injected at build time:
//
//	go build -ldflags "-X main.version=... -X main.commit=..."
var (
	version = "0.1.0-dev"
	commit  = "dev"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "circle:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := logging.New(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg.Version = version
	logger.Info("starting circle",
		"version", version,
		"commit", commit,
		"public_url", cfg.PublicURL,
	)

	database, err := db.New(ctx, cfg.DatabaseURL, cfg.DBMinConns, cfg.DBMaxConns)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer database.Close()

	if os.Getenv("CIRCLE_AUTO_MIGRATE") != "false" {
		if err := migrate.Run(ctx, database.Pool); err != nil {
			return fmt.Errorf("apply migrations: %w", err)
		}
		latest, _ := migrate.Latest(ctx, database.Pool)
		logger.Info("migrations applied", "latest", latest)
	}

	server := httpx.New(httpx.Dependencies{
		Logger:    logger,
		Version:   version,
		PublicURL: cfg.PublicURL,
		WebOrigin: cfg.WebOrigin,
		Ready:     database.Healthy,
	})

	return server.Run(ctx, cfg.HTTPAddr)
}

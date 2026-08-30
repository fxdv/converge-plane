// Command converge runs the Converge API server.
//
// Converge is a fast, self-hostable issue tracker for software teams.
// The binary starts PostgreSQL-backed services, applies schema migrations,
// and serves the JSON API.
//
// Subcommands:
//
//	converge      run the server (default)
//	converge seed  create the demo workspace and exit
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"converge/internal/api"
	"converge/internal/auth"
	"converge/internal/config"
	"converge/internal/db"
	"converge/internal/httpx"
	"converge/internal/logging"
	"converge/internal/migrate"
	"converge/internal/seed"
)

// Injected at build time:
//
//	go build -ldflags "-X main.version=... -X main.commit=..."
var (
	version = "0.1.0-dev"
	commit  = "dev"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "seed" {
		if err := runSeed(); err != nil {
			fmt.Fprintln(os.Stderr, "converge:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "converge:", err)
		os.Exit(1)
	}
}

// bootstrap loads config, logging, database, and applies migrations.
func bootstrap() (config.Config, *slog.Logger, *db.DB, context.Context, context.CancelFunc, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, nil, nil, nil, nil, fmt.Errorf("load config: %w", err)
	}
	logger := logging.New(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	cfg.Version = version
	logger.Info("starting converge", "version", version, "commit", commit, "public_url", cfg.PublicURL)

	database, err := db.New(ctx, cfg.DatabaseURL, cfg.DBMinConns, cfg.DBMaxConns)
	if err != nil {
		stop()
		return cfg, logger, nil, nil, nil, fmt.Errorf("connect database: %w", err)
	}
	if os.Getenv("CONVERGE_AUTO_MIGRATE") != "false" {
		if err := migrate.Run(ctx, database.Pool); err != nil {
			database.Close()
			stop()
			return cfg, logger, nil, nil, nil, fmt.Errorf("apply migrations: %w", err)
		}
		logger.Info("migrations applied")
	}
	return cfg, logger, database, ctx, stop, nil
}

func run() (err error) {
	cfg, logger, database, ctx, stop, err := bootstrap()
	if err != nil {
		return err
	}
	defer stop()
	defer database.Close()

	authSvc := auth.NewService(database.Pool, cfg, logger)
	apiSvc := api.New(database.Pool, cfg, logger, authSvc)

	server := httpx.New(httpx.Dependencies{
		Logger:    logger,
		Version:   version,
		PublicURL: cfg.PublicURL,
		WebOrigin: cfg.WebOrigin,
		Ready:     database.Healthy,
		MountApp:  apiSvc.Mount,
	})

	return server.Run(ctx, cfg.HTTPAddr)
}

func runSeed() error {
	_, logger, database, ctx, stop, err := bootstrap()
	if err != nil {
		return err
	}
	defer stop()
	defer database.Close()

	email, err := seed.Run(ctx, database.Pool, logger)
	if err != nil {
		return err
	}
	logger.Info("seed complete", "login_email", email)
	fmt.Printf("Demo workspace ready. Sign in with: %s\n", email)
	return nil
}

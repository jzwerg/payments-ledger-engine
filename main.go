// Command api is the payment/ledger service entry point.
//
// On startup it connects to the CockroachDB cluster, creates the `ledger`
// database and schema (idempotent migration), and serves a health endpoint.
// The double-entry posting logic and the ∑debits = ∑credits invariant live in
// internal/ledger; later milestones add the payment API, ISO 20022, and
// reconciliation on top (see PLAN.md).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jzwerg/payments-ledger-engine/internal/api"
	"github.com/jzwerg/payments-ledger-engine/internal/ledger"
)

const (
	defaultDatabaseURL = "postgresql://root@crdb-1:26257/ledger?sslmode=disable"
	defaultPort        = "8080" // container port; compose maps host 8100 -> 8080
	ledgerDatabase     = "ledger"
)

func main() {
	logger := newLogger(os.Getenv("LOG_LEVEL"))

	if err := run(logger); err != nil {
		logger.Error("startup failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	dsn := getenv("DATABASE_URL", defaultDatabaseURL)
	port := getenv("PORT", defaultPort)

	pool, err := setupDatabase(ctx, logger, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           api.NewServer(ledger.New(pool), logger).Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

// setupDatabase creates the ledger database (if absent) and applies the schema,
// returning a pool connected to the ledger database.
//
// The DATABASE_URL points at the `ledger` database, which does not exist on a
// fresh cluster — so we first connect to CockroachDB's default database to
// create it, then connect to `ledger` and migrate. Both steps are idempotent.
func setupDatabase(ctx context.Context, logger *slog.Logger, dsn string) (*pgxpool.Pool, error) {
	bootDSN, err := bootstrapDSN(dsn)
	if err != nil {
		return nil, err
	}

	bootPool, err := openPoolWithRetry(ctx, logger, bootDSN)
	if err != nil {
		return nil, fmt.Errorf("connect (bootstrap): %w", err)
	}
	if _, err := bootPool.Exec(ctx, "CREATE DATABASE IF NOT EXISTS "+ledgerDatabase); err != nil {
		bootPool.Close()
		return nil, fmt.Errorf("create database: %w", err)
	}
	bootPool.Close()
	logger.Info("database ready", "database", ledgerDatabase)

	pool, err := openPoolWithRetry(ctx, logger, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect (ledger): %w", err)
	}
	if err := ledger.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	logger.Info("schema applied")
	return pool, nil
}

// bootstrapDSN rewrites a DSN to target CockroachDB's default database, so we
// can create the ledger database before connecting to it.
func bootstrapDSN(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	u.Path = "/defaultdb"
	return u.String(), nil
}

// openPoolWithRetry dials the cluster with bounded exponential backoff. Even
// though the API waits on `crdb-init: service_completed_successfully`, a node
// may need a moment more to accept SQL connections.
func openPoolWithRetry(ctx context.Context, logger *slog.Logger, dsn string) (*pgxpool.Pool, error) {
	var lastErr error
	delay := 500 * time.Millisecond
	for attempt := 1; attempt <= 10; attempt++ {
		pool, err := pgxpool.New(ctx, dsn)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool, nil
			}
			pool.Close()
		}
		lastErr = err
		logger.Warn("database not ready, retrying", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 8*time.Second {
			delay *= 2
		}
	}
	return nil, fmt.Errorf("exhausted retries: %w", lastErr)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func newLogger(level string) *slog.Logger {
	lvl := slog.LevelInfo
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

// Command api is the payment/ledger service entry point.
//
// Milestone 0 (first boot): connect to the CockroachDB cluster, run the
// startup migration that creates the `ledger` database and the append-only
// double-entry schema, then serve a health endpoint. Business logic — the
// balance invariant, idempotency keys, ISO 20022 — lands in later milestones
// (see PLAN.md); this file deliberately stops at "the stack boots and connects".
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

	"github.com/jackc/pgx/v5"
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

	if err := migrate(ctx, logger, dsn); err != nil {
		return fmt.Errorf("migration: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

// migrate creates the ledger database (if absent) and applies the schema.
//
// The DATABASE_URL points at the `ledger` database, which does not exist on a
// fresh cluster — so we first connect to CockroachDB's default database to
// create it, then connect to `ledger` to install the schema. Both steps are
// idempotent, so a restarting API re-runs them harmlessly.
func migrate(ctx context.Context, logger *slog.Logger, dsn string) error {
	bootstrapDSN, err := bootstrapDSN(dsn)
	if err != nil {
		return err
	}

	bootstrap, err := connectWithRetry(ctx, logger, bootstrapDSN)
	if err != nil {
		return fmt.Errorf("connect (bootstrap): %w", err)
	}
	if _, err := bootstrap.Exec(ctx, "CREATE DATABASE IF NOT EXISTS "+ledgerDatabase); err != nil {
		_ = bootstrap.Close(ctx)
		return fmt.Errorf("create database: %w", err)
	}
	_ = bootstrap.Close(ctx)
	logger.Info("database ready", "database", ledgerDatabase)

	conn, err := connectWithRetry(ctx, logger, dsn)
	if err != nil {
		return fmt.Errorf("connect (ledger): %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	logger.Info("schema applied")
	return nil
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

// connectWithRetry dials the cluster with bounded exponential backoff. Even
// though the API waits on `crdb-init: service_completed_successfully`, a node
// may need a moment more to accept SQL connections.
func connectWithRetry(ctx context.Context, logger *slog.Logger, dsn string) (*pgx.Conn, error) {
	var lastErr error
	delay := 500 * time.Millisecond
	for attempt := 1; attempt <= 10; attempt++ {
		conn, err := pgx.Connect(ctx, dsn)
		if err == nil {
			if err = conn.Ping(ctx); err == nil {
				return conn, nil
			}
			_ = conn.Close(ctx)
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

package ledger

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
)

// execer is satisfied by *pgxpool.Pool, *pgx.Conn, and pgx.Tx, so Migrate can
// run against a pool at startup or a connection inside a test.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// schemaSQL is the append-only double-entry ledger schema.
//
// Milestone 1 scope: the schema plus the in-code invariant and serializable
// posting live here. The schema stays tables-only — the ∑debits = ∑credits
// invariant is enforced in PostTransaction (see ledger.go), not by a DB
// constraint, because it spans multiple rows of a single transaction.
// Idempotency keys, ISO 20022, and reconciliation are later milestones.
//
// Model:
//   - accounts      — the books money moves between; balances are *derived*
//     from entries, never stored mutably on the account.
//   - transactions  — one balanced money movement (a journal entry / payment);
//     groups the entry lines that must net to zero.
//   - entries       — immutable, append-only debit/credit lines. The ledger is
//     never updated or deleted in place; corrections are new
//     compensating entries.
//
// Everything is IF NOT EXISTS so a restarting API (or a re-run test) is
// harmless.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS accounts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       STRING NOT NULL,
    currency   STRING NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS transactions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    description STRING NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Append-only: rows are inserted, never updated or deleted.
CREATE TABLE IF NOT EXISTS entries (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID NOT NULL REFERENCES transactions (id),
    account_id     UUID NOT NULL REFERENCES accounts (id),
    direction      STRING NOT NULL CHECK (direction IN ('debit', 'credit')),
    -- Minor units (e.g. cents); positive magnitude, direction carries the sign.
    amount         INT8 NOT NULL CHECK (amount > 0),
    currency       STRING NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS entries_account_idx ON entries (account_id);
CREATE INDEX IF NOT EXISTS entries_transaction_idx ON entries (transaction_id);
`

// Migrate installs the ledger schema. It is idempotent and safe to call on
// every boot.
func Migrate(ctx context.Context, db execer) error {
	_, err := db.Exec(ctx, schemaSQL)
	return err
}

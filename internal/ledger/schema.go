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
// The schema stays tables-only — the ∑debits = ∑credits invariant is enforced
// in PostTransaction (see ledger.go), not by a DB constraint, because it spans
// multiple rows of a single transaction. ISO 20022 and reconciliation are
// later milestones.
//
// Model:
//   - accounts         — the books money moves between; balances are *derived*
//     from entries, never stored mutably on the account.
//   - transactions     — one balanced money movement (a journal entry / payment);
//     groups the entry lines that must net to zero.
//   - entries          — immutable, append-only debit/credit lines. The ledger
//     is never updated or deleted in place; corrections are new compensating
//     entries.
//   - idempotency_keys — maps a client-supplied key to the transaction it
//     produced, so a retried payment returns the original result instead of
//     posting a second movement (milestone 2, ADR 0001).
//
// Everything is IF NOT EXISTS so a restarting API (or a re-run test) is
// harmless.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS accounts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       STRING NOT NULL,
    currency   STRING NOT NULL,
    -- Optional external identifier (e.g. an IBAN) used to resolve ISO 20022
    -- parties to ledger accounts. NULL for accounts created without one.
    iban       STRING,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Add iban to accounts created before milestone 3, and make it unique. A
-- unique index treats NULLs as distinct, so accounts without an IBAN coexist.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS iban STRING;
CREATE UNIQUE INDEX IF NOT EXISTS accounts_iban_idx ON accounts (iban);

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

-- Exactly-once: a client key maps to at most one transaction. request_hash
-- lets us tell a genuine retry (same request -> replay the result) from a key
-- reused for a different request (a conflict, which we reject).
CREATE TABLE IF NOT EXISTS idempotency_keys (
    key            STRING PRIMARY KEY,
    request_hash   STRING NOT NULL,
    transaction_id UUID NOT NULL REFERENCES transactions (id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

// Migrate installs the ledger schema. It is idempotent and safe to call on
// every boot.
func Migrate(ctx context.Context, db execer) error {
	_, err := db.Exec(ctx, schemaSQL)
	return err
}

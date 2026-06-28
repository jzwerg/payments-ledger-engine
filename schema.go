package main

// schemaSQL is the append-only double-entry ledger schema.
//
// Milestone 0 scope: tables only. The balance invariant (∑debits = ∑credits),
// idempotency-key enforcement, and the concurrency guarantees described in
// ADR 0001 are deliberately NOT enforced here yet — they arrive in later
// milestones. Everything is `IF NOT EXISTS` so a restarting API is harmless.
//
// Model:
//   - accounts      — the books money moves between; balances are *derived*
//                     from entries, never stored mutably on the account.
//   - transactions  — one balanced money movement (a journal entry / payment);
//                     groups the entry lines that must net to zero.
//   - entries       — immutable, append-only debit/credit lines. The ledger is
//                     never updated or deleted in place; corrections are new
//                     compensating entries.
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

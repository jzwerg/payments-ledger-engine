// Package ledger is the append-only double-entry core (PLAN.md milestone 1).
//
// It enforces the project's central invariant — ∑debits = ∑credits for every
// transaction — in code, and posts movements inside CockroachDB serializable
// transactions with retry-on-conflict. Balances are *derived* by aggregating
// entries; no account row is ever mutated in place.
//
// Out of scope here (later milestones): idempotency keys, ISO 20022 parsing,
// reconciliation, and the kill-a-node failure demo.
package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Direction is the side of the ledger an entry posts to.
type Direction string

const (
	Debit  Direction = "debit"
	Credit Direction = "credit"
)

// Invariant / input errors. Callers can match these with errors.Is.
var (
	ErrNoEntries         = errors.New("ledger: transaction has no entries")
	ErrTooFewEntries     = errors.New("ledger: double-entry transaction needs at least one debit and one credit")
	ErrNonPositiveAmount = errors.New("ledger: entry amount must be positive")
	ErrInvalidDirection  = errors.New("ledger: entry direction must be debit or credit")
	ErrMissingAccount    = errors.New("ledger: entry is missing an account id")
	ErrMixedCurrency     = errors.New("ledger: all entries in a transaction must share one currency")
	ErrUnbalanced        = errors.New("ledger: ∑debits ≠ ∑credits")
)

// EntryInput is a single debit or credit line. Amount is a positive magnitude
// in minor units (e.g. cents); Direction carries the sign.
type EntryInput struct {
	AccountID string
	Direction Direction
	Amount    int64
	Currency  string
}

// TransactionInput is one balanced money movement: a set of entry lines whose
// debits and credits must net to zero.
type TransactionInput struct {
	Description string
	Entries     []EntryInput
}

// maxSerializableRetries bounds retries on serialization failures (SQLSTATE
// 40001). CockroachDB is serializable by default and signals write/read
// conflicts by aborting one transaction with a retryable error; the contract
// is that the client re-runs it. See docs/adr/0003.
const maxSerializableRetries = 5

// Ledger posts and reads double-entry movements against a CockroachDB pool.
type Ledger struct {
	pool *pgxpool.Pool
}

// New returns a Ledger backed by the given pool.
func New(pool *pgxpool.Pool) *Ledger {
	return &Ledger{pool: pool}
}

// CreateAccount inserts an account and returns its id.
func (l *Ledger) CreateAccount(ctx context.Context, name, currency string) (string, error) {
	var id string
	err := l.pool.QueryRow(ctx,
		`INSERT INTO accounts (name, currency) VALUES ($1, $2) RETURNING id`,
		name, currency,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create account: %w", err)
	}
	return id, nil
}

// PostTransaction validates the double-entry invariant in code, then appends
// the transaction and its entries inside a single serializable transaction,
// retrying on serialization conflicts. It returns the new transaction id.
//
// Validation happens before any write, so an unbalanced transaction never
// touches the database.
func (l *Ledger) PostTransaction(ctx context.Context, in TransactionInput) (string, error) {
	if err := validate(in); err != nil {
		return "", err
	}

	var txnID string
	err := l.inSerializableTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO transactions (description) VALUES ($1) RETURNING id`,
			in.Description,
		).Scan(&txnID); err != nil {
			return fmt.Errorf("insert transaction: %w", err)
		}
		for _, e := range in.Entries {
			if _, err := tx.Exec(ctx,
				`INSERT INTO entries (transaction_id, account_id, direction, amount, currency)
				 VALUES ($1, $2, $3, $4, $5)`,
				txnID, e.AccountID, string(e.Direction), e.Amount, e.Currency,
			); err != nil {
				return fmt.Errorf("insert entry: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return txnID, nil
}

// Balance returns an account's signed net position in minor units, computed as
// ∑debits − ∑credits over all of its entries. (Account-type-aware "natural"
// balances arrive with the chart of accounts in a later milestone; the signed
// net is unambiguous and sufficient for the invariant work here.)
func (l *Ledger) Balance(ctx context.Context, accountID string) (int64, error) {
	var net int64
	err := l.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(CASE WHEN direction = 'debit' THEN amount ELSE -amount END), 0)
		   FROM entries WHERE account_id = $1`,
		accountID,
	).Scan(&net)
	if err != nil {
		return 0, fmt.Errorf("balance: %w", err)
	}
	return net, nil
}

// inSerializableTx runs fn inside a SERIALIZABLE transaction, retrying the
// whole function on serialization failures. A non-retryable error (or a
// successful commit) ends the loop.
func (l *Ledger) inSerializableTx(ctx context.Context, fn func(pgx.Tx) error) error {
	var lastErr error
	for attempt := 1; attempt <= maxSerializableRetries; attempt++ {
		tx, err := l.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			return fmt.Errorf("begin tx: %w", err)
		}

		err = fn(tx)
		if err == nil {
			err = tx.Commit(ctx)
		}
		if err == nil {
			return nil
		}

		// Commit may have failed; rollback is a no-op then, and its error is
		// not useful, so we ignore it.
		_ = tx.Rollback(ctx)

		if isSerializationFailure(err) {
			lastErr = err
			continue
		}
		return err
	}
	return fmt.Errorf("serializable txn failed after %d attempts: %w", maxSerializableRetries, lastErr)
}

// validate enforces the double-entry invariant in code. It is pure (no DB) so
// it is unit-testable and rejects bad input before any write.
func validate(in TransactionInput) error {
	if len(in.Entries) == 0 {
		return ErrNoEntries
	}
	if len(in.Entries) < 2 {
		return ErrTooFewEntries
	}

	var debits, credits int64
	var sawDebit, sawCredit bool
	currency := in.Entries[0].Currency

	for _, e := range in.Entries {
		if e.AccountID == "" {
			return ErrMissingAccount
		}
		if e.Amount <= 0 {
			return ErrNonPositiveAmount
		}
		if e.Currency != currency {
			return ErrMixedCurrency
		}
		switch e.Direction {
		case Debit:
			debits += e.Amount
			sawDebit = true
		case Credit:
			credits += e.Amount
			sawCredit = true
		default:
			return ErrInvalidDirection
		}
	}

	if !sawDebit || !sawCredit {
		return ErrTooFewEntries
	}
	if debits != credits {
		return fmt.Errorf("%w: debits=%d credits=%d", ErrUnbalanced, debits, credits)
	}
	return nil
}

// isSerializationFailure reports whether err is CockroachDB's retryable
// serialization failure (SQLSTATE 40001).
func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40001"
	}
	return false
}

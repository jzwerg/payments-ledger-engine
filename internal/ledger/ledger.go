// Package ledger is the append-only double-entry core (PLAN.md milestone 1).
//
// It enforces the project's central invariant — ∑debits = ∑credits for every
// transaction — in code, and posts movements inside CockroachDB serializable
// transactions with retry-on-conflict. Balances are *derived* by aggregating
// entries; no account row is ever mutated in place. Posting can be made
// exactly-once with an idempotency key (PostTransactionIdempotent).
//
// Out of scope here (later milestones): ISO 20022 parsing, reconciliation, and
// the kill-a-node failure demo.
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

	// ErrMissingIdempotencyKey is returned when an idempotent post is called
	// without a key.
	ErrMissingIdempotencyKey = errors.New("ledger: idempotency key is required")
	// ErrIdempotencyConflict is returned when a key is reused with a request
	// that differs from the one it originally recorded.
	ErrIdempotencyConflict = errors.New("ledger: idempotency key reused with a different request")
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

// GetOrCreateAccountByIBAN resolves an account by its IBAN, creating it with
// the given name and currency if none exists yet. It lets ISO 20022 parties
// (debtor/creditor) map onto ledger accounts. Safe under concurrent creates:
// the unique index rejects the loser, which then re-selects the winner's row.
func (l *Ledger) GetOrCreateAccountByIBAN(ctx context.Context, iban, name, currency string) (string, error) {
	if iban == "" {
		return "", errors.New("ledger: iban is required")
	}

	var id string
	err := l.pool.QueryRow(ctx, `SELECT id FROM accounts WHERE iban = $1`, iban).Scan(&id)
	switch {
	case err == nil:
		return id, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return "", fmt.Errorf("lookup account by iban: %w", err)
	}

	err = l.pool.QueryRow(ctx,
		`INSERT INTO accounts (name, currency, iban) VALUES ($1, $2, $3) RETURNING id`,
		name, currency, iban,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	// Lost a create race: the row now exists, so re-select it.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if e := l.pool.QueryRow(ctx, `SELECT id FROM accounts WHERE iban = $1`, iban).Scan(&id); e == nil {
			return id, nil
		}
	}
	return "", fmt.Errorf("create account by iban: %w", err)
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
		id, err := insertMovement(ctx, tx, in)
		txnID = id
		return err
	})
	if err != nil {
		return "", err
	}
	return txnID, nil
}

// PostTransactionIdempotent posts a movement exactly once for the given key.
//
// The first call with a key posts the transaction, records the key, and
// returns created=true. A later call with the same key and the same
// requestHash replays the original transaction id (created=false) without
// posting again. Reusing a key with a different requestHash returns
// ErrIdempotencyConflict. Concurrent duplicates are safe: serializable
// isolation aborts the loser, and the retry finds the recorded key.
func (l *Ledger) PostTransactionIdempotent(ctx context.Context, key, requestHash string, in TransactionInput) (txnID string, created bool, err error) {
	if key == "" {
		return "", false, ErrMissingIdempotencyKey
	}
	if err := validate(in); err != nil {
		return "", false, err
	}

	var (
		resultID   string
		wasCreated bool
	)
	txErr := l.inSerializableTx(ctx, func(tx pgx.Tx) error {
		var existingID, existingHash string
		scanErr := tx.QueryRow(ctx,
			`SELECT transaction_id, request_hash FROM idempotency_keys WHERE key = $1`,
			key,
		).Scan(&existingID, &existingHash)
		switch {
		case scanErr == nil:
			// Key already used — a retry. Replay only if the request matches.
			if existingHash != requestHash {
				return ErrIdempotencyConflict
			}
			resultID, wasCreated = existingID, false
			return nil
		case errors.Is(scanErr, pgx.ErrNoRows):
			// New key — fall through and post the movement.
		default:
			return fmt.Errorf("lookup idempotency key: %w", scanErr)
		}

		id, err := insertMovement(ctx, tx, in)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO idempotency_keys (key, request_hash, transaction_id) VALUES ($1, $2, $3)`,
			key, requestHash, id,
		); err != nil {
			return fmt.Errorf("insert idempotency key: %w", err)
		}
		resultID, wasCreated = id, true
		return nil
	})
	if txErr != nil {
		return "", false, txErr
	}
	return resultID, wasCreated, nil
}

// insertMovement appends a transaction and its entries within tx, returning the
// new transaction id. Callers must validate the input first.
func insertMovement(ctx context.Context, tx pgx.Tx, in TransactionInput) (string, error) {
	var txnID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO transactions (description) VALUES ($1) RETURNING id`,
		in.Description,
	).Scan(&txnID); err != nil {
		return "", fmt.Errorf("insert transaction: %w", err)
	}
	for _, e := range in.Entries {
		if _, err := tx.Exec(ctx,
			`INSERT INTO entries (transaction_id, account_id, direction, amount, currency)
			 VALUES ($1, $2, $3, $4, $5)`,
			txnID, e.AccountID, string(e.Direction), e.Amount, e.Currency,
		); err != nil {
			return "", fmt.Errorf("insert entry: %w", err)
		}
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

		if isRetryable(err) {
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

// isRetryable reports whether err is a transaction failure worth re-running.
//
//   - 40001 (serialization_failure): CockroachDB's documented retry contract —
//     a conflicting transaction was aborted; the client re-runs it.
//   - 23505 (unique_violation): two concurrent idempotent posts raced on the
//     same key. Re-running finds the recorded key and replays its result, so
//     the operation converges instead of erroring.
func isRetryable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40001" || pgErr.Code == "23505"
	}
	return false
}

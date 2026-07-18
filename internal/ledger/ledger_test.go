package ledger

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// --- Pure invariant tests (no database; always run) ---------------------------

func TestValidate(t *testing.T) {
	const eur = "EUR"
	acctA, acctB := "acct-a", "acct-b"

	tests := []struct {
		name    string
		in      TransactionInput
		wantErr error // nil means "must pass"
	}{
		{
			name: "balanced two-line transaction",
			in: TransactionInput{Entries: []EntryInput{
				{AccountID: acctA, Direction: Debit, Amount: 1000, Currency: eur},
				{AccountID: acctB, Direction: Credit, Amount: 1000, Currency: eur},
			}},
			wantErr: nil,
		},
		{
			name: "balanced split across multiple credits",
			in: TransactionInput{Entries: []EntryInput{
				{AccountID: acctA, Direction: Debit, Amount: 1000, Currency: eur},
				{AccountID: acctB, Direction: Credit, Amount: 600, Currency: eur},
				{AccountID: "acct-c", Direction: Credit, Amount: 400, Currency: eur},
			}},
			wantErr: nil,
		},
		{
			name:    "no entries",
			in:      TransactionInput{},
			wantErr: ErrNoEntries,
		},
		{
			name: "single entry",
			in: TransactionInput{Entries: []EntryInput{
				{AccountID: acctA, Direction: Debit, Amount: 1000, Currency: eur},
			}},
			wantErr: ErrTooFewEntries,
		},
		{
			name: "all debits, no credit",
			in: TransactionInput{Entries: []EntryInput{
				{AccountID: acctA, Direction: Debit, Amount: 1000, Currency: eur},
				{AccountID: acctB, Direction: Debit, Amount: 1000, Currency: eur},
			}},
			wantErr: ErrTooFewEntries,
		},
		{
			name: "unbalanced",
			in: TransactionInput{Entries: []EntryInput{
				{AccountID: acctA, Direction: Debit, Amount: 1000, Currency: eur},
				{AccountID: acctB, Direction: Credit, Amount: 999, Currency: eur},
			}},
			wantErr: ErrUnbalanced,
		},
		{
			name: "non-positive amount",
			in: TransactionInput{Entries: []EntryInput{
				{AccountID: acctA, Direction: Debit, Amount: 0, Currency: eur},
				{AccountID: acctB, Direction: Credit, Amount: 0, Currency: eur},
			}},
			wantErr: ErrNonPositiveAmount,
		},
		{
			name: "invalid direction",
			in: TransactionInput{Entries: []EntryInput{
				{AccountID: acctA, Direction: "sideways", Amount: 1000, Currency: eur},
				{AccountID: acctB, Direction: Credit, Amount: 1000, Currency: eur},
			}},
			wantErr: ErrInvalidDirection,
		},
		{
			name: "mixed currency",
			in: TransactionInput{Entries: []EntryInput{
				{AccountID: acctA, Direction: Debit, Amount: 1000, Currency: eur},
				{AccountID: acctB, Direction: Credit, Amount: 1000, Currency: "USD"},
			}},
			wantErr: ErrMixedCurrency,
		},
		{
			name: "missing account id",
			in: TransactionInput{Entries: []EntryInput{
				{AccountID: "", Direction: Debit, Amount: 1000, Currency: eur},
				{AccountID: acctB, Direction: Credit, Amount: 1000, Currency: eur},
			}},
			wantErr: ErrMissingAccount,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validate(tc.in)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("validate() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// --- Integration tests (require a CockroachDB; skipped when unavailable) ------

// newTestLedger connects to the cluster named by LEDGER_TEST_DATABASE_URL,
// applies the schema, and returns a Ledger. Skips when the env var is unset so
// `go test ./...` passes on a machine without a database (e.g. a web session).
func newTestLedger(t *testing.T) *Ledger {
	t.Helper()
	dsn := os.Getenv("LEDGER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("LEDGER_TEST_DATABASE_URL not set; skipping DB integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(pool)
}

func TestPostTransactionDerivesBalances(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	from, err := l.CreateAccount(ctx, "alice", "EUR")
	if err != nil {
		t.Fatalf("create from account: %v", err)
	}
	to, err := l.CreateAccount(ctx, "bob", "EUR")
	if err != nil {
		t.Fatalf("create to account: %v", err)
	}

	if _, err := l.PostTransaction(ctx, TransactionInput{
		Description: "alice pays bob 10.00 EUR",
		Entries: []EntryInput{
			{AccountID: from, Direction: Debit, Amount: 1000, Currency: "EUR"},
			{AccountID: to, Direction: Credit, Amount: 1000, Currency: "EUR"},
		},
	}); err != nil {
		t.Fatalf("post transaction: %v", err)
	}

	// Balance is the signed net (debits − credits).
	if got, err := l.Balance(ctx, from); err != nil || got != 1000 {
		t.Fatalf("from balance = %d, %v; want 1000, nil", got, err)
	}
	if got, err := l.Balance(ctx, to); err != nil || got != -1000 {
		t.Fatalf("to balance = %d, %v; want -1000, nil", got, err)
	}
}

func TestPostTransactionRejectsUnbalancedWithoutWriting(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	acct, err := l.CreateAccount(ctx, "carol", "EUR")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	other, err := l.CreateAccount(ctx, "dave", "EUR")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	_, err = l.PostTransaction(ctx, TransactionInput{
		Description: "unbalanced — must be rejected",
		Entries: []EntryInput{
			{AccountID: acct, Direction: Debit, Amount: 1000, Currency: "EUR"},
			{AccountID: other, Direction: Credit, Amount: 1, Currency: "EUR"},
		},
	})
	if !errors.Is(err, ErrUnbalanced) {
		t.Fatalf("PostTransaction err = %v, want ErrUnbalanced", err)
	}

	// The rejected transaction must not have written any entries.
	if got, err := l.Balance(ctx, acct); err != nil || got != 0 {
		t.Fatalf("balance after rejected post = %d, %v; want 0, nil", got, err)
	}
}

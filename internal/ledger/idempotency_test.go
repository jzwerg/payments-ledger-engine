package ledger

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// balanced returns a balanced 1000-minor-unit movement between two accounts.
func balanced(from, to string) TransactionInput {
	return TransactionInput{
		Description: "idempotency test",
		Entries: []EntryInput{
			{AccountID: from, Direction: Credit, Amount: 1000, Currency: "EUR"},
			{AccountID: to, Direction: Debit, Amount: 1000, Currency: "EUR"},
		},
	}
}

func TestPostTransactionIdempotentReplaysSameKey(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	from, to := twoAccounts(t, l)

	move := balanced(from, to)
	const key = "idem-replay-1"
	hash := "hash-abc"

	firstID, created, err := l.PostTransactionIdempotent(ctx, key, hash, move)
	if err != nil || !created {
		t.Fatalf("first post: id=%q created=%v err=%v; want created=true, nil", firstID, created, err)
	}

	// Same key + same request: replay, no second movement.
	secondID, created, err := l.PostTransactionIdempotent(ctx, key, hash, move)
	if err != nil {
		t.Fatalf("replay post: %v", err)
	}
	if created {
		t.Fatalf("replay reported created=true; want false")
	}
	if secondID != firstID {
		t.Fatalf("replay returned txn %q; want original %q", secondID, firstID)
	}

	// The money moved exactly once.
	if got, _ := l.Balance(ctx, from); got != -1000 {
		t.Fatalf("from balance = %d; want -1000 (moved once)", got)
	}
	if got, _ := l.Balance(ctx, to); got != 1000 {
		t.Fatalf("to balance = %d; want 1000 (moved once)", got)
	}
}

func TestPostTransactionIdempotentRejectsKeyReuse(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	from, to := twoAccounts(t, l)

	const key = "idem-conflict-1"
	if _, _, err := l.PostTransactionIdempotent(ctx, key, "hash-original", balanced(from, to)); err != nil {
		t.Fatalf("first post: %v", err)
	}

	// Same key, different request fingerprint → conflict.
	_, _, err := l.PostTransactionIdempotent(ctx, key, "hash-different", balanced(from, to))
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("reused key err = %v; want ErrIdempotencyConflict", err)
	}
}

func TestPostTransactionIdempotentRequiresKey(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	from, to := twoAccounts(t, l)

	_, _, err := l.PostTransactionIdempotent(ctx, "", "hash", balanced(from, to))
	if !errors.Is(err, ErrMissingIdempotencyKey) {
		t.Fatalf("empty key err = %v; want ErrMissingIdempotencyKey", err)
	}
}

// TestPostTransactionIdempotentConcurrent fires the same keyed request from many
// goroutines at once. Exactly one must post; all must observe the same
// transaction id; the money must move exactly once. This exercises the
// serializable + retry path that makes exactly-once hold under concurrency.
func TestPostTransactionIdempotentConcurrent(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	from, to := twoAccounts(t, l)

	const (
		key         = "idem-concurrent-1"
		hash        = "hash-same"
		concurrency = 16
	)
	move := balanced(from, to)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		ids      = map[string]struct{}{}
		createds int
		firstErr error
	)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, created, err := l.PostTransactionIdempotent(ctx, key, hash, move)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			ids[id] = struct{}{}
			if created {
				createds++
			}
		}()
	}
	wg.Wait()

	if firstErr != nil {
		t.Fatalf("concurrent post error: %v", firstErr)
	}
	if createds != 1 {
		t.Fatalf("created count = %d; want exactly 1", createds)
	}
	if len(ids) != 1 {
		t.Fatalf("observed %d distinct transaction ids; want 1", len(ids))
	}
	if got, _ := l.Balance(ctx, from); got != -1000 {
		t.Fatalf("from balance = %d; want -1000 (moved exactly once)", got)
	}
}

func twoAccounts(t *testing.T, l *Ledger) (from, to string) {
	t.Helper()
	ctx := context.Background()
	var err error
	if from, err = l.CreateAccount(ctx, "from", "EUR"); err != nil {
		t.Fatalf("create from: %v", err)
	}
	if to, err = l.CreateAccount(ctx, "to", "EUR"); err != nil {
		t.Fatalf("create to: %v", err)
	}
	return from, to
}

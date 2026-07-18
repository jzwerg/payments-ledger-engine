package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jzwerg/payments-ledger-engine/internal/ledger"
)

// --- Validation tests (no database; always run) ------------------------------
//
// These paths return before the ledger is touched, so a nil-ledger Server is
// safe and they run everywhere, including a daemon-less environment.

func TestHealth(t *testing.T) {
	rec := do(t, NewServer(nil, nil), http.MethodGet, "/health", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health = %d; want 200", rec.Code)
	}
}

func TestPaymentRequiresIdempotencyKey(t *testing.T) {
	body := `{"from_account":"a","to_account":"b","amount":100,"currency":"EUR"}`
	rec := do(t, NewServer(nil, nil), http.MethodPost, "/payments", body, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing key = %d; want 400", rec.Code)
	}
}

func TestPaymentRejectsBadInput(t *testing.T) {
	hdr := map[string]string{"Idempotency-Key": "k1"}
	cases := []struct {
		name string
		body string
	}{
		{"malformed json", `{`},
		{"unknown field", `{"from_account":"a","to_account":"b","amount":1,"currency":"EUR","bogus":1}`},
		{"same account", `{"from_account":"a","to_account":"a","amount":1,"currency":"EUR"}`},
		{"non-positive amount", `{"from_account":"a","to_account":"b","amount":0,"currency":"EUR"}`},
		{"missing currency", `{"from_account":"a","to_account":"b","amount":1}`},
		{"missing accounts", `{"amount":1,"currency":"EUR"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, NewServer(nil, nil), http.MethodPost, "/payments", tc.body, hdr)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s = %d; want 400", tc.name, rec.Code)
			}
		})
	}
}

func TestPaymentRequestHashStableAndDistinct(t *testing.T) {
	a := paymentRequest{FromAccount: "x", ToAccount: "y", Amount: 100, Currency: "EUR", Description: "rent"}
	same := paymentRequest{FromAccount: "x", ToAccount: "y", Amount: 100, Currency: "EUR", Description: "rent"}
	diff := paymentRequest{FromAccount: "x", ToAccount: "y", Amount: 101, Currency: "EUR", Description: "rent"}

	if a.hash() != same.hash() {
		t.Fatalf("identical requests hashed differently")
	}
	if a.hash() == diff.hash() {
		t.Fatalf("different requests hashed the same")
	}
}

// --- Integration tests (require CockroachDB; skipped when unavailable) --------

func TestPaymentFlowIsExactlyOnce(t *testing.T) {
	srv := newTestServer(t)

	from := createAccount(t, srv, "alice", "EUR")
	to := createAccount(t, srv, "bob", "EUR")

	body := `{"from_account":"` + from + `","to_account":"` + to + `","amount":2500,"currency":"EUR","description":"invoice 7"}`
	hdr := map[string]string{"Idempotency-Key": "pay-key-1"}

	// First call creates the payment.
	rec := do(t, srv, http.MethodPost, "/payments", body, hdr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first payment = %d (%s); want 201", rec.Code, rec.Body.String())
	}
	var first paymentResponse
	decode(t, rec, &first)
	if first.IdempotentReplay {
		t.Fatalf("first payment reported idempotent_replay=true")
	}

	// Same key + same body: replay, 200, same transaction, no second movement.
	rec = do(t, srv, http.MethodPost, "/payments", body, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("replay payment = %d; want 200", rec.Code)
	}
	var replay paymentResponse
	decode(t, rec, &replay)
	if !replay.IdempotentReplay || replay.TransactionID != first.TransactionID {
		t.Fatalf("replay = %+v; want same txn as %q with idempotent_replay=true", replay, first.TransactionID)
	}

	// Same key + different body: conflict.
	other := `{"from_account":"` + from + `","to_account":"` + to + `","amount":9999,"currency":"EUR"}`
	rec = do(t, srv, http.MethodPost, "/payments", other, hdr)
	if rec.Code != http.StatusConflict {
		t.Fatalf("key reuse = %d; want 409", rec.Code)
	}

	// Money moved exactly once: source down 2500, destination up 2500.
	if got := balance(t, srv, from); got != -2500 {
		t.Fatalf("from balance = %d; want -2500", got)
	}
	if got := balance(t, srv, to); got != 2500 {
		t.Fatalf("to balance = %d; want 2500", got)
	}
}

func TestPaymentToMissingAccountIsUnprocessable(t *testing.T) {
	srv := newTestServer(t)
	from := createAccount(t, srv, "carol", "EUR")

	body := `{"from_account":"` + from + `","to_account":"00000000-0000-0000-0000-000000000000","amount":100,"currency":"EUR"}`
	rec := do(t, srv, http.MethodPost, "/payments", body, map[string]string{"Idempotency-Key": "pay-missing"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("payment to missing account = %d (%s); want 422", rec.Code, rec.Body.String())
	}
}

// --- helpers -----------------------------------------------------------------

func newTestServer(t *testing.T) *Server {
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
	if err := ledger.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewServer(ledger.New(pool), nil)
}

func do(t *testing.T, srv *Server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, r)
	return rec
}

func createAccount(t *testing.T, srv *Server, name, currency string) string {
	t.Helper()
	rec := do(t, srv, http.MethodPost, "/accounts",
		`{"name":"`+name+`","currency":"`+currency+`"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create account %q = %d (%s); want 201", name, rec.Code, rec.Body.String())
	}
	var resp accountResponse
	decode(t, rec, &resp)
	return resp.ID
}

func balance(t *testing.T, srv *Server, accountID string) int64 {
	t.Helper()
	rec := do(t, srv, http.MethodGet, "/accounts/"+accountID+"/balance", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("balance = %d (%s); want 200", rec.Code, rec.Body.String())
	}
	var resp balanceResponse
	decode(t, rec, &resp)
	return resp.Balance
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
}

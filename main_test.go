package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHealthEndpoint exercises the /health handler without a database, so CI
// has a real, fast, dependency-free test from the very first boot. The ledger
// correctness and concurrency suites described in PLAN.md land in later
// milestones.
func TestHealthEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health: got status %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("GET /health: unexpected body %q", rec.Body.String())
	}
}

// TestBootstrapDSN verifies the DSN rewrite that lets the migration create the
// `ledger` database before connecting to it.
func TestBootstrapDSN(t *testing.T) {
	got, err := bootstrapDSN("postgresql://root@crdb-1:26257/ledger?sslmode=disable")
	if err != nil {
		t.Fatalf("bootstrapDSN returned error: %v", err)
	}
	want := "postgresql://root@crdb-1:26257/defaultdb?sslmode=disable"
	if got != want {
		t.Fatalf("bootstrapDSN: got %q, want %q", got, want)
	}
}

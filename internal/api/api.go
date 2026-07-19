// Package api is the HTTP payment API (PLAN.md milestone 2).
//
// It exposes health, account, and payment endpoints on top of internal/ledger.
// Payments are exactly-once: each carries an Idempotency-Key header, so a
// retried request returns the original result instead of moving money twice.
//
// ISO 20022 (pain.001 → pacs.008) and reconciliation are later milestones; the
// payment body here is a small JSON representation, not ISO XML yet.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jzwerg/payments-ledger-engine/internal/ledger"
)

// Server holds the API dependencies and builds its routes.
type Server struct {
	ledger *ledger.Ledger
	logger *slog.Logger
}

// NewServer returns a Server backed by the given ledger.
func NewServer(l *ledger.Ledger, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{ledger: l, logger: logger}
}

// Routes returns the HTTP handler for the API.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /accounts", s.handleCreateAccount)
	mux.HandleFunc("GET /accounts/{id}/balance", s.handleBalance)
	mux.HandleFunc("POST /payments", s.handlePayment)
	mux.HandleFunc("POST /iso20022/pain001", s.handlePain001)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type createAccountRequest struct {
	Name     string `json:"name"`
	Currency string `json:"currency"`
}

type accountResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Currency string `json:"currency"`
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var req createAccountRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || req.Currency == "" {
		writeError(w, http.StatusBadRequest, "name and currency are required")
		return
	}

	id, err := s.ledger.CreateAccount(r.Context(), req.Name, req.Currency)
	if err != nil {
		s.writeLedgerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, accountResponse{ID: id, Name: req.Name, Currency: req.Currency})
}

type balanceResponse struct {
	AccountID string `json:"account_id"`
	Balance   int64  `json:"balance"` // signed net in minor units (debits − credits)
}

func (s *Server) handleBalance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "account id is required")
		return
	}
	bal, err := s.ledger.Balance(r.Context(), id)
	if err != nil {
		s.writeLedgerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, balanceResponse{AccountID: id, Balance: bal})
}

// paymentRequest moves `amount` (minor units) from one account to another. The
// source is credited and the destination debited, so the source's balance
// decreases and the destination's increases.
type paymentRequest struct {
	FromAccount string `json:"from_account"`
	ToAccount   string `json:"to_account"`
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	Description string `json:"description"`
}

type paymentResponse struct {
	TransactionID    string `json:"transaction_id"`
	Status           string `json:"status"`
	IdempotentReplay bool   `json:"idempotent_replay"`
}

func (s *Server) handlePayment(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "Idempotency-Key header is required")
		return
	}

	var req paymentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.FromAccount == "" || req.ToAccount == "" {
		writeError(w, http.StatusBadRequest, "from_account and to_account are required")
		return
	}
	if req.FromAccount == req.ToAccount {
		writeError(w, http.StatusBadRequest, "from_account and to_account must differ")
		return
	}
	if req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "amount must be positive")
		return
	}
	if req.Currency == "" {
		writeError(w, http.StatusBadRequest, "currency is required")
		return
	}

	movement := ledger.TransactionInput{
		Description: req.Description,
		Entries: []ledger.EntryInput{
			{AccountID: req.FromAccount, Direction: ledger.Credit, Amount: req.Amount, Currency: req.Currency},
			{AccountID: req.ToAccount, Direction: ledger.Debit, Amount: req.Amount, Currency: req.Currency},
		},
	}

	txnID, created, err := s.ledger.PostTransactionIdempotent(r.Context(), key, req.hash(), movement)
	if err != nil {
		s.writeLedgerError(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, paymentResponse{
		TransactionID:    txnID,
		Status:           "posted",
		IdempotentReplay: !created,
	})
}

// hash is a stable fingerprint of the payment, used to tell a genuine retry
// (same key, same request) from a key reused for a different request.
func (p paymentRequest) hash() string {
	// Field order is fixed by the struct, so JSON encoding is deterministic.
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// writeLedgerError maps a ledger/database error to an HTTP status.
func (s *Server) writeLedgerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ledger.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency key reused with a different request")
	case errors.Is(err, ledger.ErrMissingIdempotencyKey),
		errors.Is(err, ledger.ErrUnbalanced),
		errors.Is(err, ledger.ErrNonPositiveAmount),
		errors.Is(err, ledger.ErrMixedCurrency),
		errors.Is(err, ledger.ErrInvalidDirection),
		errors.Is(err, ledger.ErrMissingAccount),
		errors.Is(err, ledger.ErrTooFewEntries),
		errors.Is(err, ledger.ErrNoEntries):
		writeError(w, http.StatusBadRequest, err.Error())
	case isForeignKeyViolation(err):
		writeError(w, http.StatusUnprocessableEntity, "referenced account does not exist")
	default:
		s.logger.Error("request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// isForeignKeyViolation reports whether err is a Postgres/Cockroach FK
// violation (SQLSTATE 23503) — e.g. a payment naming an account that does not
// exist.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// --- JSON helpers ------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON decodes the request body into v, writing a 400 and returning false
// on malformed input. Unknown fields are rejected to catch client mistakes.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return false
	}
	return true
}

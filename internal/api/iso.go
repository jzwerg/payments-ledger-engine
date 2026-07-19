package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jzwerg/payments-ledger-engine/internal/iso20022"
	"github.com/jzwerg/payments-ledger-engine/internal/ledger"
)

// maxISOBody caps the accepted pain.001 payload.
const maxISOBody = 1 << 20 // 1 MiB

// isoResult carries the outcome of processing a pain.001.
type isoResult struct {
	Pacs002     []byte
	Pacs008     []byte
	GroupStatus string
	Results     []iso20022.StatusResult
}

// handlePain001 accepts an ISO 20022 pain.001 (Customer Credit Transfer
// Initiation) and responds with a pacs.002 status report. Each transfer is
// posted to the ledger exactly-once (keyed by its EndToEndId); the emitted
// pacs.008 interbank message is generated as a side effect and logged (a real
// deployment forwards it to the creditor agent).
func (s *Server) handlePain001(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxISOBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "request body too large or unreadable")
		return
	}

	res, err := s.processPain001(r.Context(), data, time.Now().UTC())
	if err != nil {
		if errors.Is(err, iso20022.ErrInvalidMessage) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.logger.Error("pain.001 processing failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.logger.Info("processed pain.001",
		"group_status", res.GroupStatus,
		"transfers", len(res.Results),
		"pacs008_bytes", len(res.Pacs008),
	)

	// The synchronous response is the pacs.002 status report.
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("X-Group-Status", res.GroupStatus)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(res.Pacs002)
}

// processPain001 parses the message, posts each transfer, and builds the
// pacs.002 (status) and pacs.008 (interbank) documents. It returns an error
// only when the message itself is invalid; per-transfer failures are reported
// as RJCT statuses within the pacs.002.
func (s *Server) processPain001(ctx context.Context, data []byte, now time.Time) (*isoResult, error) {
	msg, err := iso20022.ParsePain001(data)
	if err != nil {
		return nil, err
	}

	creDtTm := now.Format(time.RFC3339)
	results := make([]iso20022.StatusResult, 0, len(msg.Transfers))
	accepted := make([]iso20022.CreditTransfer, 0, len(msg.Transfers))
	for _, t := range msg.Transfers {
		res := s.postTransfer(ctx, t)
		results = append(results, res)
		if res.Status == iso20022.StatusAccepted {
			accepted = append(accepted, t)
		}
	}

	pacs008, err := iso20022.BuildPacs008(msg.MessageID+"-CLR", creDtTm, accepted)
	if err != nil {
		return nil, fmt.Errorf("build pacs.008: %w", err)
	}
	pacs002, err := iso20022.BuildPacs002(msg.MessageID+"-STS", creDtTm, msg.MessageID, results)
	if err != nil {
		return nil, fmt.Errorf("build pacs.002: %w", err)
	}

	return &isoResult{
		Pacs002:     pacs002,
		Pacs008:     pacs008,
		GroupStatus: iso20022.GroupStatus(results),
		Results:     results,
	}, nil
}

// postTransfer resolves the debtor/creditor accounts and posts the movement
// exactly-once, mapping the outcome to an ISO status with a reason code.
func (s *Server) postTransfer(ctx context.Context, t iso20022.CreditTransfer) iso20022.StatusResult {
	reject := func(reason string) iso20022.StatusResult {
		return iso20022.StatusResult{EndToEndID: t.EndToEndID, Status: iso20022.StatusRejected, Reason: reason}
	}

	debtor, err := s.ledger.GetOrCreateAccountByIBAN(ctx, t.DebtorIBAN, t.DebtorName, t.Currency)
	if err != nil {
		s.logger.Error("resolve debtor account", "iban", t.DebtorIBAN, "err", err)
		return reject("AC01") // IncorrectAccountNumber
	}
	creditor, err := s.ledger.GetOrCreateAccountByIBAN(ctx, t.CreditorIBAN, t.CreditorName, t.Currency)
	if err != nil {
		s.logger.Error("resolve creditor account", "iban", t.CreditorIBAN, "err", err)
		return reject("AC01")
	}

	// A credit transfer moves funds from debtor to creditor: credit the debtor
	// (their balance decreases) and debit the creditor (theirs increases).
	movement := ledger.TransactionInput{
		Description: t.Remittance,
		Entries: []ledger.EntryInput{
			{AccountID: debtor, Direction: ledger.Credit, Amount: t.Amount, Currency: t.Currency},
			{AccountID: creditor, Direction: ledger.Debit, Amount: t.Amount, Currency: t.Currency},
		},
	}

	_, _, err = s.ledger.PostTransactionIdempotent(ctx, t.EndToEndID, transferHash(t), movement)
	switch {
	case err == nil:
		return iso20022.StatusResult{EndToEndID: t.EndToEndID, Status: iso20022.StatusAccepted}
	case errors.Is(err, ledger.ErrIdempotencyConflict):
		// Same EndToEndId reused for different details.
		return reject("AM05") // Duplication
	default:
		s.logger.Error("post transfer", "end_to_end_id", t.EndToEndID, "err", err)
		return reject("NARR")
	}
}

// transferHash is a stable fingerprint of a transfer, so re-submitting the same
// EndToEndId with the same details replays (exactly-once) while a mismatch is a
// duplication conflict.
func transferHash(t iso20022.CreditTransfer) string {
	canonical := fmt.Sprintf("%s|%s|%d|%s|%s|%s",
		t.EndToEndID, t.Currency, t.Amount, t.DebtorIBAN, t.CreditorIBAN, t.Remittance)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

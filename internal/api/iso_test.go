package api

import (
	"encoding/xml"
	"net/http"
	"testing"
)

// A single-transfer pain.001 used by the ISO flow tests. Unique IBANs/E2E id so
// it is self-contained on a fresh CI database.
const painOneTransfer = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pain.001.001.09">
  <CstmrCdtTrfInitn>
    <GrpHdr><MsgId>ISOTEST-1</MsgId><CreDtTm>2026-07-19T09:30:00Z</CreDtTm><NbOfTxs>1</NbOfTxs></GrpHdr>
    <PmtInf>
      <Dbtr><Nm>Payer One</Nm></Dbtr>
      <DbtrAcct><Id><IBAN>DE00ISOTEST0000000001</IBAN></Id></DbtrAcct>
      <CdtTrfTxInf>
        <PmtId><EndToEndId>ISO-E2E-1</EndToEndId></PmtId>
        <Amt><InstdAmt Ccy="EUR">1000.00</InstdAmt></Amt>
        <Cdtr><Nm>Payee One</Nm></Cdtr>
        <CdtrAcct><Id><IBAN>FR00ISOTEST0000000002</IBAN></Id></CdtrAcct>
        <RmtInf><Ustrd>Invoice ISO-1</Ustrd></RmtInf>
      </CdtTrfTxInf>
    </PmtInf>
  </CstmrCdtTrfInitn>
</Document>`

// --- Validation (no database; always run) ------------------------------------

func TestPain001RejectsInvalidXML(t *testing.T) {
	rec := do(t, NewServer(nil, nil), http.MethodPost, "/iso20022/pain001", "not xml", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid xml = %d; want 400", rec.Code)
	}
}

func TestPain001RejectsWrongMessageType(t *testing.T) {
	body := `<?xml version="1.0"?><Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08"/>`
	rec := do(t, NewServer(nil, nil), http.MethodPost, "/iso20022/pain001", body, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong message type = %d; want 400", rec.Code)
	}
}

// --- Integration (requires CockroachDB; skipped when unavailable) -------------

func TestPain001FlowPostsAndIsIdempotent(t *testing.T) {
	srv := newTestServer(t)

	// First submission: accepted, money moves, response is a pacs.002 (ACCP).
	rec := do(t, srv, http.MethodPost, "/iso20022/pain001", painOneTransfer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("pain.001 = %d (%s); want 200", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Group-Status"); got != "ACCP" {
		t.Fatalf("group status = %q; want ACCP", got)
	}
	status := parseStatusReport(t, rec.Body.String())
	if status.GrpSts != "ACCP" || len(status.Tx) != 1 || status.Tx[0].Sts != "ACCP" {
		t.Fatalf("status report = %+v; want group+tx ACCP", status)
	}
	if status.Tx[0].EndToEndID != "ISO-E2E-1" {
		t.Fatalf("OrgnlEndToEndId = %q", status.Tx[0].EndToEndID)
	}

	// The debtor was credited (balance down) and the creditor debited (up).
	debtor := accountIDByIBAN(t, srv, "DE00ISOTEST0000000001")
	creditor := accountIDByIBAN(t, srv, "FR00ISOTEST0000000002")
	if got := balance(t, srv, debtor); got != -100000 {
		t.Fatalf("debtor balance = %d; want -100000", got)
	}
	if got := balance(t, srv, creditor); got != 100000 {
		t.Fatalf("creditor balance = %d; want 100000", got)
	}

	// Re-submitting the identical message must not move money again.
	rec = do(t, srv, http.MethodPost, "/iso20022/pain001", painOneTransfer, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Group-Status") != "ACCP" {
		t.Fatalf("resubmit = %d / %q; want 200 / ACCP", rec.Code, rec.Header().Get("X-Group-Status"))
	}
	if got := balance(t, srv, debtor); got != -100000 {
		t.Fatalf("debtor balance after resubmit = %d; want unchanged -100000", got)
	}
}

// --- helpers -----------------------------------------------------------------

type statusTx struct {
	EndToEndID string `xml:"OrgnlEndToEndId"`
	Sts        string `xml:"TxSts"`
}

type statusReport struct {
	GrpSts string
	Tx     []statusTx
}

func parseStatusReport(t *testing.T, body string) statusReport {
	t.Helper()
	var doc struct {
		Body struct {
			GrpSts string     `xml:"OrgnlGrpInfAndSts>GrpSts"`
			Tx     []statusTx `xml:"TxInfAndSts"`
		} `xml:"FIToFIPmtStsRpt"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parse pacs.002 %q: %v", body, err)
	}
	return statusReport{GrpSts: doc.Body.GrpSts, Tx: doc.Body.Tx}
}

// accountIDByIBAN resolves the ledger account created for an IBAN by reusing
// the get-or-create resolver (the account already exists after processing).
func accountIDByIBAN(t *testing.T, srv *Server, iban string) string {
	t.Helper()
	id, err := srv.ledger.GetOrCreateAccountByIBAN(t.Context(), iban, "", "EUR")
	if err != nil {
		t.Fatalf("resolve account for %s: %v", iban, err)
	}
	return id
}

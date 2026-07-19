package iso20022

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParsePain001(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "pain001.xml"))
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}

	msg, err := ParsePain001(data)
	if err != nil {
		t.Fatalf("ParsePain001: %v", err)
	}
	if msg.MessageID != "MSG-20260719-0001" {
		t.Fatalf("MessageID = %q", msg.MessageID)
	}
	if len(msg.Transfers) != 2 {
		t.Fatalf("got %d transfers; want 2", len(msg.Transfers))
	}

	first := msg.Transfers[0]
	if first.EndToEndID != "E2E-0001" {
		t.Errorf("EndToEndID = %q; want E2E-0001", first.EndToEndID)
	}
	if first.Amount != 100000 { // 1000.00 EUR -> minor units
		t.Errorf("Amount = %d; want 100000", first.Amount)
	}
	if first.Currency != "EUR" {
		t.Errorf("Currency = %q; want EUR", first.Currency)
	}
	if first.DebtorIBAN != "DE89370400440532013000" {
		t.Errorf("DebtorIBAN = %q", first.DebtorIBAN)
	}
	if first.CreditorIBAN != "FR7630006000011234567890189" {
		t.Errorf("CreditorIBAN = %q", first.CreditorIBAN)
	}
	if first.Remittance != "Invoice 2026-07-19-A" {
		t.Errorf("Remittance = %q", first.Remittance)
	}

	// The second transfer's fractional amount must not lose precision.
	if msg.Transfers[1].Amount != 25050 { // 250.50 EUR
		t.Errorf("second Amount = %d; want 25050", msg.Transfers[1].Amount)
	}
}

func TestParsePain001RejectsWrongDocument(t *testing.T) {
	// A pacs.008 document posted to the pain.001 parser must be rejected.
	pacs008 := []byte(`<?xml version="1.0"?><Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08"><FIToFICstmrCdtTrf/></Document>`)
	_, err := ParsePain001(pacs008)
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("err = %v; want ErrInvalidMessage", err)
	}
}

func TestParsePain001RejectsMissingFields(t *testing.T) {
	// Valid namespace but a transfer missing its creditor IBAN.
	bad := []byte(`<?xml version="1.0"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pain.001.001.09">
  <CstmrCdtTrfInitn>
    <GrpHdr><MsgId>M1</MsgId></GrpHdr>
    <PmtInf>
      <Dbtr><Nm>A</Nm></Dbtr>
      <DbtrAcct><Id><IBAN>DE89370400440532013000</IBAN></Id></DbtrAcct>
      <CdtTrfTxInf>
        <PmtId><EndToEndId>X1</EndToEndId></PmtId>
        <Amt><InstdAmt Ccy="EUR">10.00</InstdAmt></Amt>
        <Cdtr><Nm>B</Nm></Cdtr>
        <CdtrAcct><Id><IBAN></IBAN></Id></CdtrAcct>
      </CdtTrfTxInf>
    </PmtInf>
  </CstmrCdtTrfInitn>
</Document>`)
	_, err := ParsePain001(bad)
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("err = %v; want ErrInvalidMessage", err)
	}
}

func TestParseMinorUnits(t *testing.T) {
	ok := map[string]int64{
		"1000.00": 100000,
		"250.50":  25050,
		"10":      1000,
		"10.5":    1050,
		"0.01":    1,
		".99":     99,
	}
	for in, want := range ok {
		got, err := parseMinorUnits(in)
		if err != nil || got != want {
			t.Errorf("parseMinorUnits(%q) = %d, %v; want %d, nil", in, got, err, want)
		}
	}

	bad := []string{"", "-5.00", "+5.00", "1.005", "abc", "1,00", "0.00", "10.1x"}
	for _, in := range bad {
		if _, err := parseMinorUnits(in); err == nil {
			t.Errorf("parseMinorUnits(%q) = nil error; want error", in)
		}
	}
}

func TestFormatMinorUnitsRoundTrips(t *testing.T) {
	for _, minor := range []int64{1, 99, 100, 25050, 100000} {
		s := formatMinorUnits(minor)
		got, err := parseMinorUnits(s)
		if err != nil || got != minor {
			t.Errorf("round-trip %d -> %q -> %d, %v", minor, s, got, err)
		}
	}
}

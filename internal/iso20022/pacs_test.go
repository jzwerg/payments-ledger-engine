package iso20022

import (
	"encoding/xml"
	"strings"
	"testing"
)

func sampleTransfers() []CreditTransfer {
	return []CreditTransfer{
		{
			EndToEndID: "E2E-0001", InstructionID: "INSTR-0001",
			Amount: 100000, Currency: "EUR",
			DebtorName: "Acme GmbH", DebtorIBAN: "DE89370400440532013000",
			CreditorName: "Fournisseur SARL", CreditorIBAN: "FR7630006000011234567890189",
			Remittance: "Invoice A",
		},
		{
			EndToEndID: "E2E-0002",
			Amount:     25050, Currency: "EUR",
			DebtorName: "Acme GmbH", DebtorIBAN: "DE89370400440532013000",
			CreditorName: "Leverancier BV", CreditorIBAN: "NL91ABNA0417164300",
		},
	}
}

func TestBuildPacs008(t *testing.T) {
	out, err := BuildPacs008("MSG-CLR", "2026-07-19T09:30:00Z", sampleTransfers())
	if err != nil {
		t.Fatalf("BuildPacs008: %v", err)
	}
	if !strings.Contains(string(out), nsPacs008) {
		t.Fatalf("output missing pacs.008 namespace:\n%s", out)
	}

	// Re-parse to confirm structure and that amounts are formatted correctly.
	var doc struct {
		XMLName xml.Name
		Body    struct {
			GrpHdr struct {
				NbOfTxs string `xml:"NbOfTxs"`
			} `xml:"GrpHdr"`
			Tx []struct {
				EndToEndID string `xml:"PmtId>EndToEndId"`
				Amt        string `xml:"IntrBkSttlmAmt"`
			} `xml:"CdtTrfTxInf"`
		} `xml:"FIToFICstmrCdtTrf"`
	}
	if err := xml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("re-parse pacs.008: %v", err)
	}
	if doc.XMLName.Space != nsPacs008 || doc.XMLName.Local != "Document" {
		t.Fatalf("root = %v", doc.XMLName)
	}
	if doc.Body.GrpHdr.NbOfTxs != "2" {
		t.Fatalf("NbOfTxs = %q; want 2", doc.Body.GrpHdr.NbOfTxs)
	}
	if len(doc.Body.Tx) != 2 || doc.Body.Tx[0].EndToEndID != "E2E-0001" {
		t.Fatalf("transactions = %+v", doc.Body.Tx)
	}
	if doc.Body.Tx[0].Amt != "1000.00" {
		t.Fatalf("amount = %q; want 1000.00", doc.Body.Tx[0].Amt)
	}
}

func TestBuildPacs002(t *testing.T) {
	results := []StatusResult{
		{EndToEndID: "E2E-0001", Status: StatusAccepted},
		{EndToEndID: "E2E-0002", Status: StatusRejected, Reason: "AM05"},
	}
	out, err := BuildPacs002("MSG-STS", "2026-07-19T09:30:00Z", "MSG-ORIG", results)
	if err != nil {
		t.Fatalf("BuildPacs002: %v", err)
	}

	var doc struct {
		XMLName xml.Name
		Body    struct {
			OrgnlGrp struct {
				OrgnlMsgID string `xml:"OrgnlMsgId"`
				GrpSts     string `xml:"GrpSts"`
			} `xml:"OrgnlGrpInfAndSts"`
			Tx []struct {
				EndToEndID string `xml:"OrgnlEndToEndId"`
				Sts        string `xml:"TxSts"`
				ReasonCd   string `xml:"StsRsnInf>Rsn>Cd"`
			} `xml:"TxInfAndSts"`
		} `xml:"FIToFIPmtStsRpt"`
	}
	if err := xml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("re-parse pacs.002: %v", err)
	}
	if doc.XMLName.Space != nsPacs002 {
		t.Fatalf("namespace = %q", doc.XMLName.Space)
	}
	if doc.Body.OrgnlGrp.OrgnlMsgID != "MSG-ORIG" {
		t.Fatalf("OrgnlMsgId = %q", doc.Body.OrgnlGrp.OrgnlMsgID)
	}
	if doc.Body.OrgnlGrp.GrpSts != StatusPartial {
		t.Fatalf("GrpSts = %q; want PART", doc.Body.OrgnlGrp.GrpSts)
	}
	if len(doc.Body.Tx) != 2 {
		t.Fatalf("got %d tx statuses; want 2", len(doc.Body.Tx))
	}
	if doc.Body.Tx[0].Sts != StatusAccepted {
		t.Fatalf("tx0 status = %q; want ACCP", doc.Body.Tx[0].Sts)
	}
	if doc.Body.Tx[1].Sts != StatusRejected || doc.Body.Tx[1].ReasonCd != "AM05" {
		t.Fatalf("tx1 = %+v; want RJCT/AM05", doc.Body.Tx[1])
	}
}

func TestGroupStatus(t *testing.T) {
	cases := []struct {
		name    string
		results []StatusResult
		want    string
	}{
		{"all accepted", []StatusResult{{Status: StatusAccepted}, {Status: StatusAccepted}}, StatusAccepted},
		{"all rejected", []StatusResult{{Status: StatusRejected}}, StatusRejected},
		{"mixed", []StatusResult{{Status: StatusAccepted}, {Status: StatusRejected}}, StatusPartial},
	}
	for _, tc := range cases {
		if got := GroupStatus(tc.results); got != tc.want {
			t.Errorf("%s: GroupStatus = %q; want %q", tc.name, got, tc.want)
		}
	}
}

package iso20022

import (
	"encoding/xml"
	"fmt"
)

const nsPacs008 = "urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08"

// BuildPacs008 emits an FI-to-FI Customer Credit Transfer for the given
// accepted transfers — the interbank message a debtor agent forwards to the
// creditor agent. msgID and creDtTm identify the generated message.
func BuildPacs008(msgID, creDtTm string, transfers []CreditTransfer) ([]byte, error) {
	doc := pacs008Document{}
	doc.XMLName = xml.Name{Space: nsPacs008, Local: "Document"}
	doc.Body.GrpHdr = grpHdr008{
		MsgID:   msgID,
		CreDtTm: creDtTm,
		NbOfTxs: fmt.Sprintf("%d", len(transfers)),
		SttlmInf: sttlmInf{
			SttlmMtd: "CLRG", // cleared through a clearing system
		},
	}
	for _, t := range transfers {
		doc.Body.CdtTrfTxInf = append(doc.Body.CdtTrfTxInf, cdtTrfTxInf008{
			PmtID: pmtID008{
				InstrID:    t.InstructionID,
				EndToEndID: t.EndToEndID,
				TxID:       t.EndToEndID,
			},
			IntrBkSttlmAmt: amount{Value: formatMinorUnits(t.Amount), Ccy: t.Currency},
			Dbtr:           namedParty{Nm: t.DebtorName},
			DbtrAcct:       ibanAccount{ID: ibanID{IBAN: t.DebtorIBAN}},
			Cdtr:           namedParty{Nm: t.CreditorName},
			CdtrAcct:       ibanAccount{ID: ibanID{IBAN: t.CreditorIBAN}},
			RmtInf:         remittance(t.Remittance),
		})
	}
	return marshalDocument(doc)
}

type pacs008Document struct {
	XMLName xml.Name
	Body    fiToFICstmrCdtTrf `xml:"FIToFICstmrCdtTrf"`
}

type fiToFICstmrCdtTrf struct {
	GrpHdr      grpHdr008        `xml:"GrpHdr"`
	CdtTrfTxInf []cdtTrfTxInf008 `xml:"CdtTrfTxInf"`
}

type grpHdr008 struct {
	MsgID    string   `xml:"MsgId"`
	CreDtTm  string   `xml:"CreDtTm"`
	NbOfTxs  string   `xml:"NbOfTxs"`
	SttlmInf sttlmInf `xml:"SttlmInf"`
}

type sttlmInf struct {
	SttlmMtd string `xml:"SttlmMtd"`
}

type cdtTrfTxInf008 struct {
	PmtID          pmtID008    `xml:"PmtId"`
	IntrBkSttlmAmt amount      `xml:"IntrBkSttlmAmt"`
	Dbtr           namedParty  `xml:"Dbtr"`
	DbtrAcct       ibanAccount `xml:"DbtrAcct"`
	Cdtr           namedParty  `xml:"Cdtr"`
	CdtrAcct       ibanAccount `xml:"CdtrAcct"`
	RmtInf         *rmtInf     `xml:"RmtInf,omitempty"`
}

type pmtID008 struct {
	InstrID    string `xml:"InstrId,omitempty"`
	EndToEndID string `xml:"EndToEndId"`
	TxID       string `xml:"TxId"`
}

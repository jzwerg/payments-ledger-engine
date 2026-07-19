package iso20022

import (
	"encoding/xml"
	"fmt"
)

const nsPacs002 = "urn:iso:std:iso:20022:tech:xsd:pacs.002.001.10"

// Transaction status codes (ISO 20022 external code set).
const (
	StatusAccepted = "ACCP" // AcceptedCustomerProfile
	StatusRejected = "RJCT" // Rejected
	StatusPartial  = "PART" // PartiallyAccepted (group level)
)

// StatusResult is the outcome of processing one transfer, keyed by its
// EndToEndId. Reason is populated for rejections.
type StatusResult struct {
	EndToEndID string
	Status     string
	Reason     string
}

// GroupStatus reduces per-transfer results to a group status: ACCP if all
// accepted, RJCT if all rejected, PART otherwise.
func GroupStatus(results []StatusResult) string {
	var accepted, rejected int
	for _, r := range results {
		if r.Status == StatusAccepted {
			accepted++
		} else {
			rejected++
		}
	}
	switch {
	case rejected == 0:
		return StatusAccepted
	case accepted == 0:
		return StatusRejected
	default:
		return StatusPartial
	}
}

// BuildPacs002 emits a Payment Status Report responding to the original
// message originalMsgID with per-transfer statuses.
func BuildPacs002(msgID, creDtTm, originalMsgID string, results []StatusResult) ([]byte, error) {
	doc := pacs002Document{}
	doc.XMLName = xml.Name{Space: nsPacs002, Local: "Document"}
	doc.Body.GrpHdr = grpHdr002{MsgID: msgID, CreDtTm: creDtTm}
	doc.Body.OrgnlGrpInfAndSts = orgnlGrpInfAndSts{
		OrgnlMsgID:   originalMsgID,
		OrgnlMsgNmID: "pain.001",
		GrpSts:       GroupStatus(results),
	}
	for _, r := range results {
		ts := txInfAndSts{
			OrgnlEndToEndID: r.EndToEndID,
			TxSts:           r.Status,
		}
		if r.Status == StatusRejected && r.Reason != "" {
			ts.StsRsnInf = &stsRsnInf{Rsn: reasonCode{Cd: r.Reason}}
		}
		doc.Body.TxInfAndSts = append(doc.Body.TxInfAndSts, ts)
	}
	return marshalDocument(doc)
}

type pacs002Document struct {
	XMLName xml.Name
	Body    fiToFIPmtStsRpt `xml:"FIToFIPmtStsRpt"`
}

type fiToFIPmtStsRpt struct {
	GrpHdr            grpHdr002         `xml:"GrpHdr"`
	OrgnlGrpInfAndSts orgnlGrpInfAndSts `xml:"OrgnlGrpInfAndSts"`
	TxInfAndSts       []txInfAndSts     `xml:"TxInfAndSts"`
}

type grpHdr002 struct {
	MsgID   string `xml:"MsgId"`
	CreDtTm string `xml:"CreDtTm"`
}

type orgnlGrpInfAndSts struct {
	OrgnlMsgID   string `xml:"OrgnlMsgId"`
	OrgnlMsgNmID string `xml:"OrgnlMsgNmId"`
	GrpSts       string `xml:"GrpSts"`
}

type txInfAndSts struct {
	OrgnlEndToEndID string     `xml:"OrgnlEndToEndId"`
	TxSts           string     `xml:"TxSts"`
	StsRsnInf       *stsRsnInf `xml:"StsRsnInf,omitempty"`
}

type stsRsnInf struct {
	Rsn reasonCode `xml:"Rsn"`
}

type reasonCode struct {
	Cd string `xml:"Cd"`
}

// --- shared XML building blocks ----------------------------------------------

type amount struct {
	Value string `xml:",chardata"`
	Ccy   string `xml:"Ccy,attr"`
}

type namedParty struct {
	Nm string `xml:"Nm"`
}

type ibanAccount struct {
	ID ibanID `xml:"Id"`
}

type ibanID struct {
	IBAN string `xml:"IBAN"`
}

type rmtInf struct {
	Ustrd []string `xml:"Ustrd,omitempty"`
}

// marshalDocument renders doc with an XML declaration and indentation.
func marshalDocument(doc any) ([]byte, error) {
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("iso20022: marshal: %w", err)
	}
	return append([]byte(xml.Header), body...), nil
}

// remittance returns a populated *rmtInf, or nil when there is no remittance
// text (so the element is omitted).
func remittance(s string) *rmtInf {
	if s == "" {
		return nil
	}
	return &rmtInf{Ustrd: []string{s}}
}

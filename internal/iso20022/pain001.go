// Package iso20022 parses and builds the ISO 20022 payment messages the engine
// speaks (PLAN.md milestone 3):
//
//   - pain.001 — Customer Credit Transfer Initiation (inbound: a customer asks
//     to move money).
//   - pacs.008 — FI-to-FI Customer Credit Transfer (outbound interbank message
//     emitted for each accepted transfer).
//   - pacs.002 — Payment Status Report (the synchronous response reporting
//     ACCP/RJCT per transfer).
//
// Parsing matches on element local names, so it accepts any pain.001 minor
// version; the message type is checked against the document namespace. Amounts
// are validated and converted to integer minor units (no floats) — money is
// never represented as a float here.
package iso20022

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidMessage is returned when a document is not a well-formed,
// business-valid pain.001.
var ErrInvalidMessage = errors.New("iso20022: invalid pain.001 message")

// CreditTransfer is one normalized credit-transfer instruction extracted from a
// pain.001. Amount is in integer minor units (e.g. cents).
type CreditTransfer struct {
	EndToEndID    string
	InstructionID string
	Amount        int64
	Currency      string
	DebtorName    string
	DebtorIBAN    string
	CreditorName  string
	CreditorIBAN  string
	Remittance    string
}

// Pain001 is a normalized customer credit transfer initiation.
type Pain001 struct {
	MessageID string
	CreatedAt string
	Transfers []CreditTransfer
}

// ParsePain001 decodes and validates a pain.001 XML document.
func ParsePain001(data []byte) (*Pain001, error) {
	var doc painDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	if doc.XMLName.Local != "Document" || !strings.Contains(doc.XMLName.Space, "pain.001") {
		return nil, fmt.Errorf("%w: not a pain.001 document (namespace %q)", ErrInvalidMessage, doc.XMLName.Space)
	}

	init := doc.CstmrCdtTrfInitn
	out := &Pain001{
		MessageID: init.GrpHdr.MsgID,
		CreatedAt: init.GrpHdr.CreDtTm,
	}

	for _, pmt := range init.PmtInf {
		for _, tx := range pmt.CdtTrfTxInf {
			amount, err := parseMinorUnits(tx.Amt.InstdAmt.Value)
			if err != nil {
				return nil, fmt.Errorf("%w: transfer %q: %v", ErrInvalidMessage, tx.PmtID.EndToEndID, err)
			}
			ct := CreditTransfer{
				EndToEndID:    strings.TrimSpace(tx.PmtID.EndToEndID),
				InstructionID: strings.TrimSpace(tx.PmtID.InstrID),
				Amount:        amount,
				Currency:      strings.ToUpper(strings.TrimSpace(tx.Amt.InstdAmt.Ccy)),
				DebtorName:    strings.TrimSpace(pmt.Dbtr.Nm),
				DebtorIBAN:    strings.TrimSpace(pmt.DbtrAcct.ID.IBAN),
				CreditorName:  strings.TrimSpace(tx.Cdtr.Nm),
				CreditorIBAN:  strings.TrimSpace(tx.CdtrAcct.ID.IBAN),
				Remittance:    strings.TrimSpace(strings.Join(tx.RmtInf.Ustrd, " ")),
			}
			if err := validateTransfer(ct); err != nil {
				return nil, err
			}
			out.Transfers = append(out.Transfers, ct)
		}
	}

	if len(out.Transfers) == 0 {
		return nil, fmt.Errorf("%w: no credit transfers", ErrInvalidMessage)
	}
	return out, nil
}

func validateTransfer(ct CreditTransfer) error {
	switch {
	case ct.EndToEndID == "":
		return fmt.Errorf("%w: a transfer is missing EndToEndId", ErrInvalidMessage)
	case len(ct.Currency) != 3:
		return fmt.Errorf("%w: transfer %q has invalid currency %q", ErrInvalidMessage, ct.EndToEndID, ct.Currency)
	case ct.DebtorIBAN == "":
		return fmt.Errorf("%w: transfer %q is missing the debtor IBAN", ErrInvalidMessage, ct.EndToEndID)
	case ct.CreditorIBAN == "":
		return fmt.Errorf("%w: transfer %q is missing the creditor IBAN", ErrInvalidMessage, ct.EndToEndID)
	case ct.DebtorIBAN == ct.CreditorIBAN:
		return fmt.Errorf("%w: transfer %q has the same debtor and creditor", ErrInvalidMessage, ct.EndToEndID)
	}
	return nil
}

// parseMinorUnits converts an ISO amount string (e.g. "1000.00") to integer
// minor units, without floating point. It assumes a 2-decimal currency (the
// SEPA/UK/US anchor currencies) and rejects more than two fraction digits, a
// sign, or non-numeric input.
func parseMinorUnits(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty amount")
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return 0, fmt.Errorf("amount %q must be an unsigned decimal", s)
	}

	intPart, fracPart := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart, fracPart = s[:dot], s[dot+1:]
	}
	if intPart == "" {
		intPart = "0"
	}
	if len(fracPart) > 2 {
		return 0, fmt.Errorf("amount %q has more than 2 decimal places", s)
	}
	if !isDigits(intPart) || !isDigits(fracPart) {
		return 0, fmt.Errorf("amount %q is not a valid decimal", s)
	}
	for len(fracPart) < 2 {
		fracPart += "0"
	}

	whole, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount %q out of range", s)
	}
	frac, _ := strconv.ParseInt(fracPart, 10, 64) // 0..99, already validated
	minor := whole*100 + frac
	if minor <= 0 {
		return 0, fmt.Errorf("amount %q must be positive", s)
	}
	return minor, nil
}

// formatMinorUnits renders integer minor units back to a 2-decimal ISO amount
// string (e.g. 100000 -> "1000.00").
func formatMinorUnits(minor int64) string {
	return fmt.Sprintf("%d.%02d", minor/100, minor%100)
}

func isDigits(s string) bool {
	if s == "" {
		return true // an empty fraction is allowed ("10" -> "10.00")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// --- XML binding for parsing (matched by local name; version-agnostic) -------

type painDocument struct {
	XMLName          xml.Name
	CstmrCdtTrfInitn struct {
		GrpHdr struct {
			MsgID   string `xml:"MsgId"`
			CreDtTm string `xml:"CreDtTm"`
			NbOfTxs string `xml:"NbOfTxs"`
		} `xml:"GrpHdr"`
		PmtInf []struct {
			Dbtr        party   `xml:"Dbtr"`
			DbtrAcct    account `xml:"DbtrAcct"`
			CdtTrfTxInf []struct {
				PmtID struct {
					InstrID    string `xml:"InstrId"`
					EndToEndID string `xml:"EndToEndId"`
				} `xml:"PmtId"`
				Amt struct {
					InstdAmt struct {
						Value string `xml:",chardata"`
						Ccy   string `xml:"Ccy,attr"`
					} `xml:"InstdAmt"`
				} `xml:"Amt"`
				Cdtr     party   `xml:"Cdtr"`
				CdtrAcct account `xml:"CdtrAcct"`
				RmtInf   struct {
					Ustrd []string `xml:"Ustrd"`
				} `xml:"RmtInf"`
			} `xml:"CdtTrfTxInf"`
		} `xml:"PmtInf"`
	} `xml:"CstmrCdtTrfInitn"`
}

type party struct {
	Nm string `xml:"Nm"`
}

type account struct {
	ID struct {
		IBAN string `xml:"IBAN"`
	} `xml:"Id"`
}

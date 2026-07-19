# ADR 0004 — ISO 20022 processing

- **Status:** Accepted
- **Context:** Milestone 3 must accept real ISO 20022 payment messages, drive
  them through the ledger, and emit the standard responses — `pain.001`
  (Customer Credit Transfer Initiation) in, `pacs.008` (FI-to-FI Customer
  Credit Transfer) and `pacs.002` (Payment Status Report) out.

## Decisions

1. **Real ISO 20022 XML, not a JSON stand-in.** Messages are parsed from and
   generated as the actual ISO XML, with the correct element names and
   namespaces. Money is parsed into integer **minor units** (no floats).
2. **Structural + business validation in Go, not runtime XSD validation.**
   Parsing binds the message to typed Go structs (matched by element local
   name, so any `pain.001` minor version is accepted) and validates the fields
   the engine depends on (EndToEndId, a 3-letter currency, debtor/creditor
   IBANs, a positive amount). We do **not** run the messages through the
   published XSDs at runtime.
3. **Exactly-once reuse of the ledger.** Each credit transfer is posted through
   milestone 2's idempotent path, keyed by its **EndToEndId** with a fingerprint
   of the transfer. Re-submitting the same message replays; reusing an
   EndToEndId for different details is a duplication conflict (`AM05`).
4. **`pacs.002` is the synchronous response; `pacs.008` is a side effect.**
   Posting `pain.001` returns the status report. The `pacs.008` interbank
   message is generated for the accepted transfers and logged — in a real
   deployment it is forwarded to the creditor agent.

## Rationale

- Using the real XML (namespaces, `InstdAmt`/`Ccy`, `EndToEndId`) is the point
  of the milestone: it proves the engine speaks the actual standard, and keeps
  the door open to true schema validation later.
- Go has no first-class XSD validator; the credible options are cgo bindings to
  libxml2 (a runtime dependency that complicates the container and CI) or an
  immature pure-Go validator. Binding to typed structs plus explicit business
  checks catches the errors that matter here without that weight. Full XSD
  validation can be layered on later without changing the interface.
- Keying idempotency on EndToEndId matches how the scheme itself identifies a
  transfer end-to-end, so retries at the message layer and at the ledger layer
  agree.

## Consequences

- Amounts assume a 2-decimal currency (the SEPA/UK/US anchor currencies);
  0- or 3-decimal currencies would need a per-currency exponent.
- Accounts are resolved from party IBANs, creating a ledger account on first
  sight of an IBAN — a demo convenience, not how account opening works in a
  real institution.
- Rejections carry ISO reason codes (`AC01`, `AM05`, `NARR`); the set can grow
  as more failure modes are handled.

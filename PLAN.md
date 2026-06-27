# Build Plan — Payments & Ledger Engine

## Goal

A double-entry ledger and payment-initiation service that processes ISO 20022 messages correctly, idempotently, and safely under concurrency, with reconciliation against an external statement.

## Definition of done

`docker-compose up` accepts a `pain.001` initiation, moves balanced funds through the ledger, and emits a `pacs.008`; the concurrency test passes, proving exactly-once handling of duplicate and simultaneous payments.

## Milestones

1. **Double-entry ledger** — append-only entries, derived balances, the ∑debits = ∑credits invariant enforced in code and via a database constraint/test.
2. **Payment initiation API** — idempotency keys + serializable (or optimistic-locked) transactions for exactly-once semantics.
3. **ISO 20022 processing** — parse `pain.001` (initiation), emit `pacs.008` (interbank), handle `pacs.002` (status).
4. **Reconciliation engine** — match a mock external statement against the ledger and flag breaks.
5. **Failure demo** — concurrency test firing duplicate + simultaneous payments; proves no double-spend, no drift.
6. **Polish** — README architecture diagram, ADRs, GitHub Actions CI with a meaningful test suite.

## Key technical challenges

- Enforcing the balance invariant under concurrent writes without sacrificing throughput.
- Choosing and justifying an isolation/locking strategy (serializable vs optimistic).
- Parsing and validating real ISO 20022 XML schemas rather than a hand-waved JSON stand-in.

## Decisions captured

- **Double-entry ledger** as the core data model — see `docs/adr/0001-ledger-and-idempotency.md`.
- **Idempotency + isolation strategy** for exactly-once payments — see same ADR.

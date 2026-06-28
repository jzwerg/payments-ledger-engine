# Build Plan — Payments & Ledger Engine

## Goal

A double-entry ledger on CockroachDB that processes ISO 20022 messages correctly, idempotently, and safely under concurrency — and stays correct when a database node fails. Reconciliation against an external statement on top.

## Definition of done

`docker-compose up` brings up a multi-node CockroachDB cluster and the API. A `pain.001` initiates a payment that moves balanced funds through the ledger and emits a `pacs.008`. The headline failure demo passes: **kill a Cockroach node during a concurrent load test — `∑debits = ∑credits` still holds, no double-spend, no lost entries.**

## Milestones

1. **Ledger on CockroachDB** — append-only double-entry schema, derived balances, the `∑debits = ∑credits` invariant enforced in code and asserted in tests; serializable transactions.
2. **Payment API** — accepts `pain.001`, enforces idempotency keys, applies balanced movements in serializable transactions.
3. **ISO 20022 processing** — parse `pain.001`, emit `pacs.008`, handle `pacs.002` status.
4. **Reconciliation engine** — match a mock external statement against the ledger; flag breaks.
5. **Failure demos** — (a) concurrency test firing duplicate + simultaneous payments (no double-spend); (b) kill a Cockroach node mid-load (invariant holds, cluster recovers).
6. **Polish** — README diagram, ADRs, GitHub Actions CI with a meaningful test suite.

## Key technical challenges

- Enforcing the balance invariant under concurrent writes across a multi-node cluster without sacrificing throughput.
- Choosing and justifying the isolation strategy (Cockroach is serializable by default; understand the retry-on-conflict implications).
- Parsing and validating real ISO 20022 XML schemas rather than a hand-waved JSON stand-in.

## Decisions captured

- **Double-entry ledger + exactly-once semantics** — `docs/adr/0001-ledger-and-idempotency.md`.
- **CockroachDB as the ledger store** — `docs/adr/0002-cockroachdb-ledger-store.md`.
- **Serializable transactions with retry-on-conflict** — `docs/adr/0003-serializable-transactions-and-retry.md`.

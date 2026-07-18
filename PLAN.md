# Build Plan — Payments & Ledger Engine

## Goal

A double-entry ledger on CockroachDB that processes ISO 20022 messages correctly, idempotently, and safely under concurrency — and stays correct when a database node fails. Reconciliation against an external statement on top.

## Definition of done

`docker-compose up` brings up a multi-node CockroachDB cluster and the API. A `pain.001` initiates a payment that moves balanced funds through the ledger and emits a `pacs.008`. The headline failure demo passes: **kill a Cockroach node during a concurrent load test — `∑debits = ∑credits` still holds, no double-spend, no lost entries.**

## Milestones

0. ✅ **First boot** — `make up` brings up the cluster + API; startup migration creates the `ledger` DB and schema; `/health` on :8100. (See `MILESTONE.md`.)
1. ✅ **Ledger on CockroachDB** — append-only double-entry schema, derived balances, the `∑debits = ∑credits` invariant enforced in code and asserted in tests; serializable transactions with retry-on-conflict (`internal/ledger`, ADR 0003).
2. ✅ **Payment API** — `POST /payments` applies balanced movements in serializable transactions and enforces idempotency keys (exactly-once: retries replay, key-reuse conflicts). Supporting endpoints: `POST /accounts`, `GET /accounts/{id}/balance` (`internal/api`). *Accepts a JSON payment body for now; the `pain.001` XML form arrives in milestone 3.*
3. ▶️ **NEXT — ISO 20022 processing** — parse `pain.001`, emit `pacs.008`, handle `pacs.002` status.
4. **Reconciliation engine** — match a mock external statement against the ledger; flag breaks.
5. **Failure demos** — (a) concurrency test firing duplicate + simultaneous payments (no double-spend); (b) kill a Cockroach node mid-load (invariant holds, cluster recovers).
6. **Polish** — README diagram, ADRs, GitHub Actions CI with a meaningful test suite.

### Current status

Milestones 0–2 are done and green in CI. **Next up: milestone 3 — ISO 20022 processing.** Scope it to: an `internal/iso20022` package that parses a real `pain.001` (customer credit transfer initiation) XML document, maps it onto a payment (reusing the milestone-2 idempotent posting path), emits a `pacs.008` (FI-to-FI credit transfer) message, and records `pacs.002` (payment status report) status. Prefer validating against the real ISO 20022 XSDs over a hand-rolled JSON stand-in (see "Key technical challenges").

## Key technical challenges

- Enforcing the balance invariant under concurrent writes across a multi-node cluster without sacrificing throughput.
- Choosing and justifying the isolation strategy (Cockroach is serializable by default; understand the retry-on-conflict implications).
- Parsing and validating real ISO 20022 XML schemas rather than a hand-waved JSON stand-in.

## Decisions captured

- **Double-entry ledger + exactly-once semantics** — `docs/adr/0001-ledger-and-idempotency.md`.
- **CockroachDB as the ledger store** — `docs/adr/0002-cockroachdb-ledger-store.md`.
- **Serializable transactions with retry-on-conflict** — `docs/adr/0003-serializable-transactions-and-retry.md`.

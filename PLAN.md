# Build Plan — Payments & Ledger Engine

## Goal

A payments core that is correct in both its *state* and its *process*: a distributed double-entry ledger on CockroachDB, and a Temporal-orchestrated payment saga, processing ISO 20022 messages idempotently and surviving both database-node and worker failures.

## Definition of done

`docker-compose up` brings up a CockroachDB cluster, a Temporal server, workers, and the API. A `pain.001` initiates a payment that flows through the saga, moves balanced funds in the ledger, and emits a `pacs.008`. Two failure demos pass:

1. **State:** kill a Cockroach node during a concurrent load test — `∑debits = ∑credits` still holds, no double-spend.
2. **Process:** kill a Temporal worker mid-saga — the workflow resumes and completes exactly once; a forced downstream failure triggers compensation with no orphaned reservation.

## Milestones

1. **Ledger on CockroachDB** — append-only double-entry schema, derived balances, the `∑debits = ∑credits` invariant enforced in code and asserted in tests; serializable transactions.
2. **Payment API** — accepts `pain.001`, enforces idempotency keys, starts a Temporal workflow.
3. **Temporal payment saga** — activities: reserve funds → dispatch `pacs.008` → await `pacs.002` status → settle; compensation (release reservation) on failure; retries + timers on each activity.
4. **ISO 20022 processing** — parse `pain.001`, emit `pacs.008`, handle `pacs.002`.
5. **Reconciliation engine** — match a mock external statement against the ledger; flag breaks.
6. **Failure demos** — (a) kill a Cockroach node under load; (b) kill a Temporal worker mid-saga + force a compensating failure.
7. **Polish** — README diagram, ADRs, GitHub Actions CI with a meaningful test suite.

## Key technical challenges

- Enforcing the balance invariant under concurrent writes across a multi-node cluster without sacrificing throughput.
- Designing ledger activities to be **idempotent** so Temporal's at-least-once activity execution never double-applies a movement.
- Keeping the boundary clean: Temporal owns workflow state, Cockroach owns financial state — the saga must never rely on Temporal to store balances, nor on the ledger to track workflow progress.

## Decisions captured

- **Double-entry ledger + exactly-once semantics** — `docs/adr/0001-ledger-and-idempotency.md`.
- **CockroachDB as the ledger store** — `docs/adr/0002-cockroachdb-ledger-store.md`.
- **Temporal for the payment saga** — `docs/adr/0003-temporal-payment-saga.md`.

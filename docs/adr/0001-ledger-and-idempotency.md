# ADR 0001 — Double-entry ledger and exactly-once payment semantics

- **Status:** Accepted
- **Context:** A payment engine must represent balances correctly and must not double-process a payment when requests are retried or arrive concurrently.

## Decision

1. Model money movement as an **immutable, append-only double-entry ledger**; balances are *derived* from entries, never mutated in place.
2. Guarantee **exactly-once** payment processing with **idempotency keys** plus **serializable transactions** (falling back to optimistic locking on hot rows where throughput demands it).

## Rationale

### Double-entry ledger
- Double-entry is the centuries-proven model for correctness: every movement is a balanced pair of debits and credits, so ∑debits = ∑credits is an invariant we can assert continuously.
- Append-only storage gives a complete, auditable history — essential for payments and reconciliation, and impossible to silently corrupt.
- Derived balances eliminate a whole class of update-anomaly bugs.

### Idempotency + isolation
- Networks retry. Without idempotency keys, a retried `pain.001` becomes a second payment.
- Serializable isolation prevents lost-update and write-skew anomalies that would let two concurrent payments overdraw an account.
- Where serializable isolation is too costly on hot accounts, optimistic locking (version checks) provides the same correctness with better throughput, at the price of retry-on-conflict.

## Consequences

- Reads of "current balance" require aggregating entries (or maintaining a materialized balance with the same transactional guarantees) — a deliberate tradeoff favoring correctness and auditability.
- The concurrency test suite becomes a first-class deliverable: it is the proof that the invariants actually hold.

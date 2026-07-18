# ADR 0003 — Serializable transactions with retry-on-conflict

- **Status:** Accepted
- **Context:** ADR 0001 requires exactly-once, anomaly-free posting; ADR 0002
  chose CockroachDB. We must say *how* posting code uses the database so the
  ∑debits = ∑credits invariant holds under concurrency.

## Decision

1. Post every transaction inside a single **`SERIALIZABLE`** database
   transaction. We do not weaken isolation per-statement.
2. The application **retries** a transaction that aborts with a serialization
   failure (**SQLSTATE `40001`**), up to a bounded number of attempts.
3. Enforce the balance invariant **in application code** (validate ∑debits =
   ∑credits, single currency, positive amounts, ≥1 debit and ≥1 credit) *before*
   any write, so an unbalanced transaction never reaches the database.

## Rationale

- CockroachDB is `SERIALIZABLE` by default — the strongest isolation, which
  rules out the write-skew and lost-update anomalies that would let two
  concurrent payments overdraw an account. Using the default keeps the
  guarantee from being an opt-in we might forget.
- Serializable isolation has a price: the database may abort a transaction that
  conflicts and ask the client to retry. That retry is the client's
  responsibility, so a small retry loop on `40001` is a required part of
  correct posting, not an optimization.
- The invariant spans multiple rows of one transaction, so a single-row `CHECK`
  constraint cannot express it. Validating in code (a pure, unit-tested
  function) is the clearest place to enforce it and gives precise errors.

## Consequences

- Posting is `O(retries)` in the worst case under high contention; the retry
  bound caps tail latency at the cost of surfacing a clear error if a
  transaction can't commit within the bound.
- The invariant is asserted in two places — the pure `validate` unit tests
  (always run) and the DB-backed integration tests (run in CI against a real
  CockroachDB). The concurrency/double-spend stress test and the node-kill
  demo build on this in later milestones (PLAN.md milestone 5).
- READ COMMITTED (available in CockroachDB) was **not** adopted: it would
  reintroduce the very anomalies ADR 0001 exists to prevent.

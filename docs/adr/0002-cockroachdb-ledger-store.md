# ADR 0002 — CockroachDB as the ledger store

- **Status:** Accepted
- **Context:** ADR 0001 commits to a double-entry ledger with serializable, exactly-once semantics. We must choose the database that backs it.

## Decision

Use **CockroachDB** as the ledger store, relying on its serializable isolation and multi-node fault tolerance.

## Rationale

- **Serializable by default.** ADR 0001 requires serializable isolation to prevent write-skew and lost-update anomalies that would let concurrent payments overdraw an account. CockroachDB provides `SERIALIZABLE` as its default isolation level — the guarantee we need is the out-of-the-box behavior, not an opt-in we might forget.
- **Survives node loss.** A ledger is the last thing that should lose data. Cockroach replicates and reaches consensus (Raft) across nodes, so a node failure does not lose committed entries or violate the balance invariant — which gives us a compelling, reviewer-visible failure demo (kill a node mid-load-test).
- **Honest distributed-systems story.** A distributed, strongly-consistent ledger is a realistic payments-infra design, not a contrived one. The same feature set (and `REGIONAL BY ROW`) also opens a future data-residency extension.

## Alternatives considered

- **PostgreSQL with explicit `SERIALIZABLE`.** Fully correct and simpler to operate (single container), but Postgres defaults to weaker isolation (must be opted into per transaction) and offers no built-in multi-node fault tolerance — so the "survives node kill" demo isn't available without bolting on replication/failover.

## Consequences

- More operational weight than a single Postgres container (a real cluster in Docker Compose).
- Reads of "current balance" aggregate entries (or use a transactionally-maintained materialized balance) — a deliberate tradeoff favoring correctness and auditability, unchanged from ADR 0001.
- Some Postgres-specific SQL features differ in Cockroach; schema/queries are written to Cockroach's supported surface.

# ADR 0003 — Temporal for the payment saga

- **Status:** Accepted
- **Context:** A payment is a multi-step process (reserve funds → dispatch interbank message → await status → settle) that can fail or stall at any step and must not be left in a partial state. We must choose how to orchestrate it durably.

## Decision

Orchestrate the payment as a **Temporal workflow (saga)**: each step is an activity with retries and timeouts, and failures trigger **compensating activities** (e.g. release a fund reservation). Temporal owns workflow/process state; CockroachDB (ADR 0002) owns financial state.

## Rationale

- **Durable execution.** If a worker crashes mid-payment, Temporal resumes the workflow exactly where it left off from its event history — no manual recovery, no orphaned half-payments. This is the single most valuable property for money movement and the basis of a strong failure demo.
- **Built-in retries, timeouts, and timers.** Awaiting a `pacs.002` status that may take seconds or hours is a first-class Temporal primitive; doing this with cron + a state table is fragile and verbose.
- **Saga / compensation as a pattern, not plumbing.** Temporal makes compensating transactions explicit and testable, which is exactly the rollback semantics a payment needs when a downstream step fails after funds were reserved.

## Alternatives considered

- **Hand-rolled state machine in Postgres/Cockroach + a worker loop.** Possible, but reimplements retries, timers, crash recovery, and idempotent step replay — i.e. a worse version of what Temporal already provides.
- **A message queue (e.g. SQS/Kafka) + consumers.** Good for fan-out, but provides no durable workflow state, no built-in compensation, and no point-in-time resume.

## Consequences

- Activities must be **idempotent**: Temporal guarantees at-least-once activity execution, so a ledger movement activity must use the payment's idempotency key to avoid double-applying on retry. This is consistent with ADR 0001 and is enforced at the ledger boundary.
- A clean boundary is required: Temporal must never be used as the source of truth for balances, and the ledger must never be used to track workflow progress. Crossing this boundary is the main design risk and is called out in PLAN.md.
- Added operational components (Temporal server + workers) in Docker Compose.

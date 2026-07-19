# Build Plan — Payments & Ledger Engine

## Goal

A double-entry ledger on CockroachDB that processes ISO 20022 messages correctly, idempotently, and safely under concurrency — and stays correct when a database node fails. Reconciliation against an external statement on top.

## Definition of done

`docker-compose up` brings up a multi-node CockroachDB cluster and the API. A `pain.001` initiates a payment that moves balanced funds through the ledger and emits a `pacs.008`. The headline failure demo passes: **kill a Cockroach node during a concurrent load test — `∑debits = ∑credits` still holds, no double-spend, no lost entries.**

## Milestones

0. ✅ **First boot** — `make up` brings up the cluster + API; startup migration creates the `ledger` DB and schema; `/health` on :8100. (See `MILESTONE.md`.)
1. ✅ **Ledger on CockroachDB** — append-only double-entry schema, derived balances, the `∑debits = ∑credits` invariant enforced in code and asserted in tests; serializable transactions with retry-on-conflict (`internal/ledger`, ADR 0003).
2. ✅ **Payment API** — `POST /payments` applies balanced movements in serializable transactions and enforces idempotency keys (exactly-once: retries replay, key-reuse conflicts). Supporting endpoints: `POST /accounts`, `GET /accounts/{id}/balance` (`internal/api`). *Accepts a JSON payment body for now; the `pain.001` XML form arrives in milestone 3.*
3. ✅ **ISO 20022 processing** — `POST /iso20022/pain001` parses a real `pain.001`, posts each transfer through the milestone-2 idempotent path (keyed by EndToEndId), returns a `pacs.002` status report, and emits a `pacs.008` interbank message (`internal/iso20022`, ADR 0004).
4. ▶️ **NEXT — Reconciliation engine** — match a mock external statement against the ledger; flag breaks.
5. **Failure demos** — (a) concurrency test firing duplicate + simultaneous payments (no double-spend); (b) kill a Cockroach node mid-load (invariant holds, cluster recovers).
6. **Polish** — README diagram, ADRs, GitHub Actions CI with a meaningful test suite.

### Current status

Milestones 0–3 are done and green in CI. **Next up: milestone 4 — reconciliation engine.** Scope it to: an `internal/reconciliation` package that ingests a mock external bank statement (e.g. an ISO 20022 `camt.053` account statement, or a simple statement-line format) and matches its entries against the ledger's derived positions, flagging **breaks** — statement lines with no matching ledger entry, ledger entries missing from the statement, and amount mismatches. Expose it (a `POST /reconciliation` run or a `make` target) and assert, in tests, that a clean statement reconciles with zero breaks and that injected discrepancies are each flagged with the right break type. Reuse the append-only ledger as the source of truth; do not mutate entries to "fix" a break — reconciliation reports, it does not correct.

**How to continue (next session):** work on branch `claude/product-thinking-repos-cmbegm` (create it from `main` if it doesn't exist — it is deleted when each milestone's PR merges). `main` holds the completed milestones; confirm milestone 3 has merged (PR #6) before starting so `internal/iso20022` is present. Keep the established rhythm: build, `gofmt`/`go vet`/`go build ./...`, `go test ./...` (unit tests pass locally; DB-backed tests skip without `LEDGER_TEST_DATABASE_URL` and run in CI against CockroachDB v24.1.5), `docker compose config -q`, then commit and push. Update this section to mark milestone 4 done and point to milestone 5 when finished.

## Key technical challenges

- Enforcing the balance invariant under concurrent writes across a multi-node cluster without sacrificing throughput.
- Choosing and justifying the isolation strategy (Cockroach is serializable by default; understand the retry-on-conflict implications).
- Parsing and validating real ISO 20022 XML schemas rather than a hand-waved JSON stand-in.

## Decisions captured

- **Double-entry ledger + exactly-once semantics** — `docs/adr/0001-ledger-and-idempotency.md`.
- **CockroachDB as the ledger store** — `docs/adr/0002-cockroachdb-ledger-store.md`.
- **Serializable transactions with retry-on-conflict** — `docs/adr/0003-serializable-transactions-and-retry.md`.
- **ISO 20022 processing** — `docs/adr/0004-iso20022-processing.md`.

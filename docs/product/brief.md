# Product Brief — Payments & Ledger Engine

> Engineering decisions live in [`docs/adr/`](../adr/). This brief is the product
> counterpart: who it's for, what success looks like, and what we deliberately
> don't build.

## The problem
Moving money is the one place fintech cannot be "mostly right." Yet teams routinely
either build a ledger in-house — and discover double-spend bugs, balance drift, and
no story for node failure *in production, where the errors are irreversible* — or
bolt money movement onto a generic database and hit the same correctness cliffs. A
ledger that is wrong under concurrency or a node failure isn't a slow product; it's a
broken one.

## Who it's for
This is **infrastructure — a platform primitive**, so its customer is another team:
- **Primary user:** the payments-platform / fintech backend engineer who needs to
  record money movement correctly and speak ISO 20022, and wants to build *on top* of
  a trustworthy core rather than debug the ledger.
- **Secondary user:** finance / ops, who rely on the reconciliation engine to surface
  breaks.
- **Economic buyer:** the CTO / VP Eng who personally carries the risk of a ledger
  bug.

## Job to be done
"Give me a money-movement core I can trust to never double-spend or drift — even
under concurrency and node failure — so I can build products on top instead of
debugging the ledger."

## What success looks like
- **North Star — zero correctness defects under adversarial conditions:** no
  invariant violation (`∑debits = ∑credits`, no double-spend, no lost entries) across
  every concurrency and failure test. For a ledger, *trust is the product* — the
  North Star is provable correctness, not raw throughput.
- **Value metrics:** reconciliation breaks auto-detected vs. missed · integration
  time for a new ISO 20022 message type · the throughput at which correctness still
  holds (the real spec is "correct *and* fast enough").
- **Quality / engineering metrics:** invariant asserted under load · exactly-once
  under duplicate + concurrent payments · clean recovery after node loss.

## Non-goals (what we deliberately don't build)
- **Not a full payment-scheme connector.** A mock counterparty stands in for real
  rails; real scheme integration comes later.
- **Not fraud / AML screening** — that's a separate concern (see the federated-AML
  project). This is the ledger, not the cop.
- **Not a general-ledger / accounting (ERP) system** — it's a money-*movement*
  ledger, not your books.
- **Not multi-currency FX / settlement netting** in the demo (a documented
  extension, not a hidden gap).
- **Not a UI product** — a backend primitive with an API.

## Sequencing — prove the riskiest assumption first
The riskiest bet is: **can the invariant hold under concurrency *and* node failure
without sacrificing acceptable throughput?**

1. Double-entry ledger + `∑debits = ∑credits` enforced in code + serializable
   transactions. *(The correctness core.)*
2. Idempotency keys + a concurrency test firing duplicate and simultaneous payments —
   no double-spend.
3. Kill a CockroachDB node mid-load — invariant holds, cluster recovers. *(The
   headline.)*
4. ISO 20022 processing + reconciliation engine on top.

Correctness primitive first; rails and reconciliation second.

## Key risks & assumptions
- **"Why not just Postgres?" (the build-vs-buy question).** A reviewer will ask why a
  single Postgres with `SERIALIZABLE` isn't enough. The honest answer: CockroachDB
  earns its place only when you need node/region-failure survival and horizontal
  scale; for single-node, Postgres is simpler. Naming that boundary *is* the product
  judgment ([ADR 0002](../adr/0002-cockroachdb-ledger-store.md)).
- **The throughput tax of correctness.** Serializable isolation + retry-on-conflict
  has a cost; the product question is whether it's acceptable — so we measure it
  rather than hand-wave.
- **Adoption through boring trust.** Infra wins by being unremarkable and provably
  safe; the demo must make correctness *visceral* (kill a node live), not claimed.
- **ISO 20022 realism.** A hand-waved JSON stand-in convinces no one — parsing real
  XML schemas is the credibility bet ([ADR 0001](../adr/0001-ledger-and-idempotency.md)).

## The demo, framed as a user outcome
A concurrent load test fires duplicate and simultaneous payments while a CockroachDB
node is killed mid-flight. The load keeps running. When it settles: `∑debits =
∑credits`, every committed payment applied exactly once, nothing lost, nothing
double-spent. *The one thing a ledger must never get wrong, proven under the exact
conditions that usually break it.*

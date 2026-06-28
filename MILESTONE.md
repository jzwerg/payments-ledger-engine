# Milestone 0 — First `docker compose up`

The single goal of this milestone: **`make up` brings the stack to a healthy,
initialised state.** This is *not* the headline node-kill demo (that's the next
milestone) and *not* ISO 20022 or reconciliation. Keep scope tight.

## Definition of done
- `cp .env.example .env && make up` boots a 3-node CockroachDB cluster; `crdb-init`
  forms the cluster and exits 0; all three nodes report healthy.
- The `api` service builds (needs a **Go Dockerfile**) and starts, connects to the
  cluster, creates the `ledger` database + schema (a startup migration), and serves
  a health endpoint on **:8100**.
- `make down` tears everything down cleanly (`-v` removes volumes).

## Smoke check (how you know it worked)
- `curl -fsS localhost:8100/health` → `200`.
- CockroachDB admin UI loads at `http://localhost:8180`.
- `make ps` shows `crdb-1/2/3` healthy and `crdb-init` exited 0.

## Explicitly out of scope (later milestones)
The double-entry invariant under load, idempotency keys, the kill-a-node demo,
ISO 20022 parsing, the reconciliation engine. See `PLAN.md`.

## Stack gotchas
- `crdb-init` must run **once** and not restart (`restart: "no"` — already set).
- The API already waits on `crdb-init: service_completed_successfully`; keep that.
- Connect with the Postgres wire protocol: `postgresql://root@crdb-1:26257/ledger?sslmode=disable`.
- Pin CockroachDB to `v24.1.5` (already in compose); use Go `stable` to match CI.

## Shared conventions (portfolio-wide — keep identical across all four repos)
- **Branch:** `claude/product-thinking-repos-cmbegm`.
- **Task interface:** `make up` / `down` / `demo` / `test` / `logs`.
- **First boot needs no secrets** — `.env.example` defaults must boot.
- **Compose v2:** `docker compose` (space), not the deprecated `docker-compose`.
- **Host ports:** this project owns the **81xx** range.
- **Validate without a daemon:** `docker compose config -q` parses the stack even
  where Docker can't run (e.g. a Claude Code web session); a real boot must be
  verified on a machine with a Docker daemon.

## Paste-ready session kickoff
> Get this repo to its first `docker compose up` state per `MILESTONE.md`. Add a Go
> Dockerfile and a minimal API with a `/health` endpoint that connects to the
> CockroachDB cluster and runs a startup migration creating the `ledger` DB +
> double-entry schema. Don't build the node-kill demo, ISO 20022, or reconciliation
> yet. Validate with `docker compose config -q`, then confirm the smoke check.
> Commit to `claude/product-thinking-repos-cmbegm` and push.

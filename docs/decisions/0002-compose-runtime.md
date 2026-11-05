# 0002. How the Compose stack runs

- Status: Accepted
- Date: 2026-10-28

## Context

`docker compose up` must bring up git2 on one machine (WBS-13.1, M0 demo on 30 Oct). Later it is what `git2ctl install` runs (WBS-13.2), so the defaults must be safe for a real server, not only for a laptop.

## Decision

**Start order.** `postgres` and `redis` have health checks. The Postgres check uses TCP (`pg_isready -h 127.0.0.1`), because during first start the image runs a temporary socket-only server that a socket check would report as ready. A one-shot `migrate` service applies git2's `migrations/` with git2's own `helpers/dev/migrate.sh` and exits. App services wait with `depends_on: condition: service_healthy` for the data stores and `condition: service_completed_successfully` for `migrate` ([Compose spec, depends_on](https://docs.docker.com/reference/compose-file/services/#depends_on)). `migrate.sh` records applied files in `schema_migrations`, so it runs on every start and does nothing when there is nothing new.

**PostgreSQL 18.** The newest major, supported until November 2030. The 18 image stores data under `/var/lib/postgresql/18/docker` and declares the volume at `/var/lib/postgresql`, so the volume is mounted there ([docker-library/postgres](https://github.com/docker-library/postgres)). Changing the image to a new major does **not** upgrade: the new server initializes an empty cluster in its own directory, and the services come up on an empty database. Major upgrades need dump and restore (or an explicit `pg_upgrade`); `git2ctl upgrade` will do this (W3–W4). The dev database stays the shared Supabase project; this Postgres is the one an installed instance uses.

**Redis without persistence** (`--save "" --appendonly no`). The project plan says Redis holds no durable state and is not part of the backup, and git2-runner's ADR 0003 builds on that. Turning persistence off makes the rule visible. Losing Redis signs users out and resets rate-limit counters. It also forgets `revoked:<jti>` entries, so an access token revoked in the last `JWT_EXPIRY` (15 min) works again until it expires. That is accepted: the window is bounded by the access token's lifetime. Same image as the devcontainers (`redis:7.4-alpine`).

**Nothing internal is published.** PostgreSQL, Redis and git-svc's gRPC port are reachable only on the Compose network. git2 publishes `127.0.0.1:8080` until the gateway (WBS-1.2) terminates TLS in front of it; `GIT2_BIND=0.0.0.0` opts in to other machines.

**Hardening on every git2 service** ([Docker security docs](https://docs.docker.com/engine/security/), [OWASP Docker Security Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Docker_Security_Cheat_Sheet.html)):
- non-root user (distroless `nonroot` for git2, uid 10001 for git-svc);
- read-only root file system, with a volume or a tmpfs where a service must write;
- `cap_drop: [ALL]` and `no-new-privileges`.

**The password stays out of process arguments.** `migrate.sh` calls `psql "$POSTGRES_DSN"` several times, so the migrate service gets a DSN without a password plus `PGPASSWORD`, which libpq reads from the environment. `ps` on the host no longer shows the password.

**Fail closed on secrets.** `POSTGRES_PASSWORD` and `JWT_SECRET` use `${VAR:?message}`, so `docker compose` refuses to start without them instead of starting with an empty password.

**git-svc is opt-in** (`--profile git-svc`) until it is on git2-codeplane's main and its migration (git2 PR #6) is merged; without that migration it stops at its startup schema check.

## Consequences

- One `docker compose up -d --build` gives a working sign-in on a clean machine with Docker. `tests/smoke/compose_smoke.sh` checks each point above, including that a published Postgres port makes it fail.
- Secrets are passed as environment variables because git2 and git-svc read only the environment. Docker recommends file-based secrets; when the services can read `*_FILE` variables, switch to Compose `secrets:`.
- All services connect as the database owner `git2`. git2-codeplane's ADR 0006 asks for a role with data privileges only. That needs default privileges on schemas that migrations create later, and is planned with `git2ctl install` (W3–W4).
- One flat network, and Redis without a password: any container can reach the database and write session keys. git-svc and later backup-svc have no gRPC authentication either. Split networks (edge, backend, data), a Redis password and per-service roles are planned with `git2ctl install` (W3–W4).
- `migrate.sh` has no lock. Two stacks migrating one database at once can make the second fail (each file runs in one transaction, so nothing is half-applied). One Compose project per database avoids it.
- `git2` and `git-svc` have no container health check: the distroless image has no shell, and git-svc has no health RPC yet (assumption B-04).
- Base images are pinned to a minor tag, not a digest. Digest pins and update PRs (Renovate or Dependabot) come with the image pipeline (WBS-13.4).

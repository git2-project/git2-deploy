# Compose stack

How git2 runs on one machine with Docker Compose (`compose/compose.yaml`). This page is for the other lanes: what is running, how services reach each other, and what to tell Deploy when your service joins.

## Run it

```bash
cd compose
cp env.example .env && chmod 600 .env   # set POSTGRES_PASSWORD and JWT_SECRET (openssl rand -hex 24 / -hex 32)
docker compose up -d --build
curl http://127.0.0.1:8080/healthz          # {"status":"ok"}
```

Add `--profile git-svc` to also start git-svc. The images are built from your local checkouts next to this repo (`../git2`, `../git2-codeplane`). To build a branch from GitHub instead:

```bash
GIT2_SRC='https://github.com/git2-project/git2.git#feat/my-branch' docker compose up -d --build
```

`docker compose down` stops everything and keeps the data. `docker compose down -v` also deletes the database and the repositories.

## Services

| Service | Image | Listens on | Reachable from | Data |
|---|---|---|---|---|
| `postgres` | `postgres:18-alpine` | 5432 | Compose network only | volume `postgres-data` |
| `redis` | `redis:7.4-alpine` | 6379 | Compose network only | none (persistence off) |
| `migrate` | `ghcr.io/git2-project/git2-migrate` | — | runs once, then exits | — |
| `git2` | `ghcr.io/git2-project/git2` | 8080 (HTTP) | host `127.0.0.1:${GIT2_HTTP_PORT:-8080}` | — |
| `git-svc` (profile `git-svc`) | `ghcr.io/git2-project/git-svc` | 9090 (gRPC) | Compose network only | volume `repositories` at `/var/lib/git2` |

Start order: `postgres` and `redis` healthy → `migrate` exits 0 → `git2` and `git-svc` start. If a migration fails, no service starts; `docker compose logs migrate` shows which file.

Inside the network, services reach each other by service name: `postgres:5432`, `redis:6379`, `git-svc:9090`.

## Settings (`compose/.env`)

| Variable | Default | Meaning |
|---|---|---|
| `POSTGRES_PASSWORD` | — (required) | Password of the `git2` database user. Use hex: it goes into a connection URL. Applied only when the database volume is first created. Changing it later locks every service out; change it in Postgres first (`ALTER ROLE`). |
| `JWT_SECRET` | — (required) | auth-svc's signing key, at least 32 characters. A shorter one makes git2 exit at start and restart in a loop. |
| `GIT2_BIND` | `127.0.0.1` | Host address git2 listens on. `0.0.0.0` for other machines (plain HTTP until the gateway lands). |
| `GIT2_HTTP_PORT` | `8080` | Host port for git2. |
| `GIT2_SRC` | `../../git2` | git2 source: a path relative to `compose/` or a Git URL with `#ref`. |
| `CODEPLANE_SRC` | `../../git2-codeplane` | git2-codeplane source, same rules. |
| `GIT2_TAG` | `dev` | Tag for the image names. |
| `GIT2_ENVIRONMENT` | `prod` | git2's `ENVIRONMENT`. |

Every service gets the same database URL in `POSTGRES_DSN`: `postgres://git2:<password>@postgres:5432/git2?sslmode=disable`.

## Rules every git2 service gets

These apply to the images built here (git2, migrate, git-svc). The official `postgres` and `redis` images run as their own non-root users after start-up; they are not read-only.

- Runs as a non-root user, with a read-only root file system, no Linux capabilities and `no-new-privileges`. Write only to the volume or tmpfs Deploy gives you, and to stdout for logs.
- Configuration comes from environment variables only.
- No published ports. Only `git2` (later: only the gateway) is reachable from outside the machine.
- Your schema comes from git2's `migrations/`. The `migrate` service applies it before you start, so a startup schema check (like git-svc's) is safe.
- Redis may be empty at any time. Prefix your keys with your service name.

## Known limits (W1)

- **Rate limits by IP.** Behind a Docker published port, git2 often sees every client as the Docker bridge address (on Docker Desktop: `172.x.0.1`), so the per-IP sign-in limits are shared by all clients on that host. The gateway (W2–W3) fixes this with `X-Real-IP`.
- **Postgres major upgrades.** Changing `postgres:18-alpine` to a new major does not upgrade the data: the new server starts an empty cluster next to the old one, and git2 comes up empty. Until `git2ctl upgrade` handles it (W3–W4), upgrade with `pg_dump` from the old version and restore into the new one.
- **Everything shares one database role and one network,** and Redis has no password. Least-privilege roles and split networks are planned with `git2ctl install` (W3–W4).

## Adding your service

Open an issue on git2-deploy, or note it in your version note under "Deploy (Ceyda)", with:

1. the repo and `cmd/` path of the binary, and whether it needs CGO or tools at run time (git-svc needs `git` ≥ 2.38);
2. the ports it listens on and who calls each one;
3. its environment variables and defaults;
4. where it writes (a volume, a tmpfs), and whether that data must be in the backup;
5. its health or readiness check, if it has one.

Deploy adds `images/<name>/Dockerfile`, the Compose service, and later the Helm templates.

## Checking it

`tests/smoke/compose_smoke.sh` (or `make tests`) brings the stack up in a throwaway project and checks everything on this page: missing secrets are refused, migrations run once, sign-in works end to end, Postgres and Redis have no host ports, git2 is non-root and read-only, and data survives `down` + `up`. `--with-git-svc` also creates a repository over gRPC.

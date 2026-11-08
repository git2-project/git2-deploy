# Compose stack

How git2 runs on one machine with Docker Compose (`compose/compose.yaml`). This page is for the other lanes: what is running, how services reach each other, and what to tell Deploy when your service joins.

## Run it

```bash
cd compose
cp env.example .env && chmod 600 .env   # set POSTGRES_PASSWORD and JWT_SECRET (openssl rand -hex 24 / -hex 32)
docker compose up -d --build
curl http://127.0.0.1:8080/healthz          # the gateway: {"status":"ok"}
open http://127.0.0.1:8080/                 # Dash; Admin at /admin/
```

Add `--profile git-svc` to also start git-svc (its Smart HTTP health check needs git2-codeplane#5). The images are built from your local checkouts next to this repo (`../git2`, `../git2-codeplane`). To build a branch from GitHub instead:

```bash
GIT2_SRC='https://github.com/git2-project/git2.git#feat/my-branch' docker compose up -d --build
```

`docker compose down` stops everything and keeps the data. `docker compose down -v` also deletes the database and the repositories.

## Services

| Service | Image | Listens on | Reachable from | Data |
|---|---|---|---|---|
| `postgres` | `postgres:18-alpine` | 5432 | Compose network only | volume `postgres-data` |
| `redis` | `redis:7.4-alpine` | 6379 | Compose network only | none (persistence off) |
| `migrate` | `ghcr.io/git2-project/git2-migrate` | none | runs once, then exits | none |
| `git2` | `ghcr.io/git2-project/git2` | 8080 (HTTP) | Compose network only | none |
| `gateway` | `ghcr.io/git2-project/git2-gateway` | 8000 (HTTP) | host `127.0.0.1:${GIT2_HTTP_PORT:-8080}`, the only published port | none (Dash and Admin are in the image) |
| `git-svc` (profile `git-svc`) | `ghcr.io/git2-project/git-svc` | 9090 (gRPC), 9080 (Smart HTTP) | Compose network only | volume `repositories` at `/var/lib/git2` |

Start order: `postgres` and `redis` healthy → `migrate` exits 0 → `git2` and `git-svc` start → `gateway` starts. Until git2 answers, the gateway returns 503 on `/api`. If a migration fails, no service starts; `docker compose logs migrate` shows which file.

Inside the network, services reach each other by service name: `postgres:5432`, `redis:6379`, `git2:8080`, `git-svc:9090`, `git-svc:9080`. Routes and headers through the gateway: [gateway.md](gateway.md).

## Settings (`compose/.env`)

| Variable | Default | Meaning |
|---|---|---|
| `POSTGRES_PASSWORD` | — (required) | Password of the `git2` database user. Use hex: it goes into a connection URL. Applied only when the database volume is first created. Changing it later locks every service out; change it in Postgres first (`ALTER ROLE`). |
| `JWT_SECRET` | — (required) | auth-svc's signing key, at least 32 characters. A shorter one makes git2 exit at start and restart in a loop. |
| `GIT2_BIND` | `127.0.0.1` | Host address the gateway listens on. For other machines use TLS (below), where it defaults to `0.0.0.0`. |
| `GIT2_HTTP_PORT` | `8080` | Host port of the gateway. With TLS: the redirect port, default `80`. |
| `GIT2_HTTPS_PORT` | `443` | With TLS only: the HTTPS port. |
| `GIT2_EXTERNAL_URL` | — (required with TLS) | `https://<host>[:port]`, the address users type. HTTP redirects go here. |
| `GIT2_TLS_DIR` | — (required with TLS) | Directory with `fullchain.pem` and `privkey.pem`, readable by uid 65532. |
| `GIT2_SUBNET` | `172.31.250.0/24` | Subnet of the Compose network. git2 core believes `X-Real-IP` only from it (`TRUSTED_PROXY_CIDRS`). Change it if it clashes with a host network. |
| `GIT2_SRC` | `../../git2` | git2 source: a path relative to `compose/` or a Git URL with `#ref`. |
| `CODEPLANE_SRC` | `../../git2-codeplane` | git2-codeplane source, same rules. |
| `GIT2_TAG` | `dev` | Tag for the image names. |
| `GIT2_ENVIRONMENT` | `prod` | git2's `ENVIRONMENT`. |

Every service gets the same database URL in `POSTGRES_DSN`: `postgres://git2:<password>@postgres:5432/git2?sslmode=disable`.

## TLS

```sh
docker compose -f compose.yaml -f compose.tls.yaml up -d
```

`compose.tls.yaml` turns on TLS at the gateway (FR-02, NFR-04): HTTPS on `GIT2_HTTPS_PORT`, TLS 1.2 and newer, `Strict-Transport-Security` for a year. Plain HTTP on `GIT2_HTTP_PORT` answers only with `308` to `GIT2_EXTERNAL_URL`; `308` keeps the method, so a `git push` to `http://` is not replayed as a GET. It replaces the loopback port of `compose.yaml`.

- The certificate files are read at start. Restart the gateway after a renewal: `docker compose -f compose.yaml -f compose.tls.yaml restart gateway`.
- certbot's `live/` directory holds symlinks into `archive/`, which a bind mount does not follow. Copy `fullchain.pem` and `privkey.pem` to `GIT2_TLS_DIR` on each renewal.
- `git2ctl install` (WBS-13.2) will write these settings; this file is what it uses.

## Rules every git2 service gets

These apply to the images built here (gateway, git2, migrate, git-svc). The official `postgres` and `redis` images run as their own non-root users after start-up; they are not read-only.

- Runs as a non-root user, with a read-only root file system, no Linux capabilities and `no-new-privileges`. Write only to the volume or tmpfs Deploy gives you, and to stdout for logs.
- Configuration comes from environment variables only.
- No published ports. Only the gateway is reachable from outside the machine.
- Every container on the Compose network may set `X-Real-IP` for git2 core and reach git-svc's 9080, which trusts `X-Git2-User-Id`. Only git2 services join this network; CI job containers never do (git2-runner A-09).
- Your schema comes from git2's `migrations/`. The `migrate` service applies it before you start, so a startup schema check (like git-svc's) is safe.
- Redis may be empty at any time. Prefix your keys with your service name.

## Known limits (W1)

- **Rate limits by IP.** git2 core now sees the client the gateway saw (`X-Real-IP`, B-02). Behind Docker's port publishing that is often the Docker bridge address (on Docker Desktop: `172.31.250.1`), so clients on one host can still share a bucket. The gateway on the host network (with TLS, so it can face clients) would give the real address; not done yet.
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

`tests/smoke/compose_smoke.sh` (or `make tests`) brings the stack up in a throwaway project and checks everything on this page: missing secrets are refused, migrations run once, sign-in works end to end through the gateway, only the gateway has a host port, every git2 container is non-root and read-only, data survives `down` + `up`, the gateway serves both web apps and refuses an API call without a token before git2 core sees it, and git2 core sees the client's IP, not the gateway's or a spoofed one. `--with-git-svc` also creates a repository over gRPC and waits for 9080 to be healthy.

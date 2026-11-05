# What we assume about the other repos

Each item names its owner and the week it must be settled by. When an owner confirms or changes one, update it here.

| ID | Assumption | Owner | By | Status |
|---|---|---|---|---|
| [B-01](#b-01--repository-bundles-come-from-git-svc) | Repository bundles come from git-svc | Apo | W4 | open |
| [B-02](#b-02--the-client-ip-behind-the-gateway) | The client IP behind the gateway | git2 owners | W2 | open |
| [B-03](#b-03--every-service-reads-only-the-environment) | Every service reads only the environment | everyone | W3 | open |
| [B-04](#b-04--health-endpoints) | Health endpoints | everyone | W3 | open |
| [B-05](#b-05--reserved-owner-names) | Reserved owner names | Beste (auth-svc) | W2 | open |

Weeks: W1 starts 26 Oct 2026.

---

### B-01 · Repository bundles come from git-svc

**We assume:** git-svc gets a server-streaming RPC that returns one repository as a Git bundle (`git bundle create --all`), and one that creates a repository from a bundle for restore. backup-svc never reads git-svc's volume, as git-svc's README asks.
**If wrong:** backup-svc mounts the repositories volume read-only and runs `git bundle` itself, which breaks the "only git-svc touches repository files" rule.

### B-02 · The client IP behind the gateway

**We assume:** git2 core takes the client IP for rate limits from `X-Real-IP` when the connection comes from the gateway, and from `RemoteAddr` otherwise. Today `internal/ratelimit.clientIP` reads only `RemoteAddr`, and its comment says the gateway rewrites it, which only works if the gateway runs in the same process.
**If wrong:** behind a separate gateway, every client shares one rate-limit bucket, and one noisy client locks everyone out of sign-in.

### B-03 · Every service reads only the environment

**We assume:** services take configuration from environment variables, with the names already in use (`POSTGRES_DSN`, `POSTGRES_MAX_CONNS`, `REDIS_ADDR`, `REDIS_PASSWORD`), and keep no state on local disk outside a volume Deploy gives them. git2's binary also reads a `.env` file if one exists; the images contain none.
**If wrong:** we add config files to the images and to the Helm chart.

### B-04 · Health endpoints

**We assume:** every HTTP service answers `GET /healthz` (process up) and, from W3, `GET /readyz` (dependencies reachable). gRPC services implement `grpc.health.v1.Health`. Compose and Helm probes use them.
**If wrong:** git-svc has no health check today, so Compose waits only for it to start, and Helm cannot tell when it is ready.

### B-05 · Reserved owner names

**We assume:** auth-svc refuses usernames and organization names that collide with top-level paths: `api`, `admin`, `healthz`, `readyz`, `assets`, `static`, `login`, `logout`, `settings`, `explore`, `new` (list in [gateway.md](../api/gateway.md#routes)). Today it has no such list.
**If wrong:** a user called `admin` has a profile page the gateway serves as the Admin app, and their repositories' web pages are unreachable.

---

## Answers to other lanes

### git2-runner (Osman), assumptions about Deploy

| ID | Answer |
|---|---|
| A-05 | Confirmed. `GIT2_CI_SECRETS_KEY` is in `secrets/` of every snapshot ([snapshot-format.md](../api/snapshot-format.md)), and restore writes it back before ci-svc starts. |
| A-06 | Confirmed. One bucket URL in `gocloud.dev/blob` form. You own `ci/`, backup-svc owns `backups/`. Snapshots do not copy the bucket; restore reuses it, and a bucket migration skips `ci/cache/`. |
| A-08 | Confirmed. ci-svc runs as its own process and image; backup-svc does the same (ADR 0003). |
| A-09 | Confirmed. The runner port is not behind the gateway; Compose publishes it on its own port, and Helm gives it its own Service. |
| A-15 | Confirmed. One Redis in Compose (`redis:6379` on the network) with persistence off, not in the backup. `REDIS_ADDR` / `REDIS_PASSWORD` as in git2. |

### git2-codeplane (Apo), version note of 26 Oct, "Deploy (Ceyda)"

| Item | Answer |
|---|---|
| Image with git ≥ 2.38 | Done: `images/git-svc`, Alpine 3.22 with git 2.49. |
| Volume for `GIT2_STORAGE_ROOT` | Done: volume `repositories` at `/var/lib/git2`, one git-svc replica. |
| `POSTGRES_DSN`, `POSTGRES_MAX_CONNS` | Done for `POSTGRES_DSN`. The data-only database role is open (plan §3). |
| Port 9090 inside the network only | Done: `expose`, no published port. |
| Backup: `repositories/` yes, `tmp/` and `home/` no, `trash/` optional | Agreed; we leave `trash/` out. We ask for bundles instead of a file copy (B-01). |
| Gateway: resolve `/<owner>/<repo>.git` to IDs, forward by ID | In the gateway contract ([gateway.md](../api/gateway.md)), W2–W3. |

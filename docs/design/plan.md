# Platform & Backup lane plan (draft v0)

Owner: Ceyda · Backup reviewer: Osman · 2026-10-28

How the deploy lane gets from an empty repo to the M4 restore demo (18 Dec) and the release on 25 Dec. Decisions are ADRs in [`docs/decisions/`](../decisions/). What we assume about the other repos is in [assumptions.md](assumptions.md).

## 1. What we owe

From the project plan (§2, §4). The project documents are in git2 under `docs/project_docs/`.

| WBS | What | Weeks | Done when | Status |
|---|---|---|---|---|
| 1.1 | Contracts v0 | W1 | `backup.v1` proto and the gateway contract merged and referenced from git2's `contracts/` | this PR |
| 13.1 | Dockerfiles and Compose | W1 | `docker compose up` starts all services, PostgreSQL and Redis on one host (M0) | this PR |
| 1.2 | Gateway: TLS, routing, sign-in check, both UIs | W2–W3 | Both UIs and all APIs reachable through one HTTPS endpoint; unauthenticated API calls rejected | |
| 13.2 | `git2ctl install` and `upgrade` | W3–W4 | A working instance on a clean Linux host with Docker | |
| 12.1 | backup-svc v0: encrypted snapshot | W4–W5 | Database, repositories, git2.yaml and secrets in one age archive with manifest and checksums | |
| 1.4 | Redis cache and rate limits | W5 | Gateway limits per user/IP; hot reads cached with expiry | |
| 12.2 | Bucket drivers | W6 | S3-compatible and local disk; nightly schedule | |
| 13.3 | Helm chart | W6–W7 | One Deployment per service on k3d/kind | |
| 12.3 | Restore to a fresh server | W7–W8 | `git2ctl restore` brings back repos, users, settings (M4) | |
| 16.1 | Install and admin guide | W8 | Someone outside the lane installs git2 from it | |
| 18.7 | Software Test Report (editor) | W8 | Submitted 21.12 | |
| 18.4 | Requirement Specification, our section | W1 | Sent to Bilge (editor) before 02.11 | draft |

Risks we own: **R-09** (a restore misses a secret) and the restore half of every "Redis holds nothing durable" promise.

Targets that touch us: `git2ctl install` in under 10 minutes on a clean host; a restore reproduces the instance exactly; amd64 and arm64; any S3-compatible bucket or local disk.

## 2. Where the project documents disagree

- **Where backup-svc lives.** The plan and git2's README: git2 repo. The team map: git2-deploy. → **Here** ([ADR 0003](../decisions/0003-backup-svc-and-its-contract-live-here.md)), like ci-svc in git2-runner.
- **Redis.** The kickoff deck says no Redis; the charter, the plan and git2-runner's ADR 0003 say one shared Redis. → One Redis in Compose and Helm, with persistence off ([ADR 0002](../decisions/0002-compose-runtime.md)).
- **Where image recipes live.** Not stated anywhere. → Here, built from the source repos ([ADR 0001](../decisions/0001-images-built-here-from-source-repos.md)).
- **The "Fork TUI".** The team map gives this lane a TUI for a git GUI app. The project plan gives the Python CLI and TUI (WBS-14) to Abdullah. → We follow the plan, which is what is graded; the question goes to the W2 sprint planning.
- **Migrations.** The team map says a GitHub Action applies migrations to the dev DB after merge. An installed instance has no GitHub Action. → The `migrate` service applies the same files with git2's `migrate.sh` on every start.

## 3. Open

- Database role with data privileges only (git2-codeplane ADR 0006). Planned with `git2ctl install`.
- File-based secrets (`*_FILE`) instead of environment variables, once the services read them.
- Digest-pinned base images and update PRs, with the image pipeline (WBS-13.4, Osman).
- ADR 0004 (gateway as its own process) needs the team's agreement at the W2 planning.

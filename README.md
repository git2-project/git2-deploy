# git2-deploy

> Everything needed to install, upgrade, back up and restore a [git2](https://github.com/git2-project/git2) instance.

**Repos:** [git2](https://github.com/git2-project/git2) · [git2-codeplane](https://github.com/git2-project/git2-codeplane) · [git2-runner](https://github.com/git2-project/git2-runner) · [git2-deploy](https://github.com/git2-project/git2-deploy)

## What lives here

| Path | What it is |
| :--- | :--- |
| `compose/` | Docker Compose file for the all-in-one, single-machine install |
| `images/` | One Dockerfile per image, built from the source repos |
| `proto/backup/v1/` | `backup.v1`, the gRPC contract of backup-svc (generated Go code in `gen/go/`) |
| `docs/api/` | Reference for the other lanes: Compose, gateway, backup, snapshot format |
| `tests/` | End-to-end checks against a real Compose stack |
| `git2ctl/` | The operator CLI. `git2ctl install` takes a machine from zero to a running instance (W3–W4) |
| `helm/` | Helm chart for multi-node Kubernetes installs (W6–W7) |

## The three promises

1. **One command to install:** `git2ctl install` brings up the whole stack.
2. **One bucket for everything:** encrypted nightly snapshots of the database, repos, `git2.yaml` and secrets go to any S3, GCS, R2 or local bucket, and a new server restores from that bucket alone.
3. **Friendly for everyone:** Dash for developers, Admin for IT.

All instance configuration lives in one file, `git2.yaml`.

## Status

Early development, on the 9-week plan in the [org README](https://github.com/git2-project). Working today: the Compose stack (PostgreSQL, Redis, migrations, git2 core, git-svc) and contract v0 of backup-svc and the gateway. The lane plan is in [`docs/design/plan.md`](docs/design/plan.md).

## Quick start

Clone `git2` (and `git2-codeplane` for git-svc) next to this repo, then:

```bash
cd compose
cp env.example .env            # set POSTGRES_PASSWORD and JWT_SECRET
docker compose up -d --build   # add --profile git-svc for the Git engine
curl http://127.0.0.1:8080/healthz
```

`make tests` runs the smoke test, which brings up a throwaway stack and checks sign-in, isolation, hardening and persistence. Details: [`docs/api/compose.md`](docs/api/compose.md).

## Contributing

- Work stays inside this repo's scope. Changes to shared contracts go through a PR approved by every affected owner.
- Every PR ships with tests and needs one human review before merge.

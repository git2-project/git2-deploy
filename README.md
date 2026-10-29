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

Early development, on the 9-week plan in the [org README](https://github.com/git2-project). This is the scaffold: repo conventions, the devcontainer, the lane plan and the first decisions. The Compose stack and contract v0 of backup-svc and the gateway come next (W1). The lane plan is in [`docs/design/plan.md`](docs/design/plan.md).

## Development

Clone `git2` and `git2-codeplane` next to this repo, then open it in the devcontainer (VS Code: *Reopen in Container*). It has Go, the Docker CLI with Compose and its own Docker daemon, so test stacks stay off your host. Docker Desktop with Compose v2 on the host works too.

## Contributing

- Work stays inside this repo's scope. Changes to shared contracts go through a PR approved by every affected owner.
- Every PR ships with tests and needs one human review before merge.

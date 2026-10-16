# git2-deploy

> Everything needed to install, upgrade, back up and restore a [git2](https://github.com/git2-project/git2) instance.

**Repos:** [git2](https://github.com/git2-project/git2) · [git2-codeplane](https://github.com/git2-project/git2-codeplane) · [git2-runner](https://github.com/git2-project/git2-runner) · [git2-deploy](https://github.com/git2-project/git2-deploy)

## What lives here

| Path | What it is |
| :--- | :--- |
| `git2ctl/` | The operator CLI. `git2ctl install` takes a machine from zero to a running instance |
| `helm/` | Helm chart for multi-node Kubernetes installs |
| `compose/` | Docker Compose file for the all-in-one, single-machine install |

## The three promises

1. **One command to install:** `git2ctl install` brings up the whole stack.
2. **One bucket for everything:** encrypted nightly snapshots of the database, repos, `git2.yaml` and secrets go to any S3, GCS, R2 or local bucket, and a new server restores from that bucket alone.
3. **Friendly for everyone:** Dash for developers, Admin for IT.

All instance configuration lives in one file, `git2.yaml`.

## Status

Early development, on the 9-week plan in the [org README](https://github.com/git2-project). Nothing here is ready to run yet; build and test commands will land in this file and in `CLAUDE.md` as the code does.

## Contributing

- Work stays inside this repo's scope. Changes to shared contracts go through a PR approved by every affected owner.
- Every PR ships with tests and needs one human review before merge.

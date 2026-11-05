# 0003. backup-svc and its contract live in git2-deploy

- Status: Accepted
- Date: 2026-10-28

## Context

The project plan and git2's README put `backup-svc/` in the git2 repo. The team map (the newest document) gives the deploy lane "the backup strategy, bucket migration and restore logic" in git2-deploy, and makes Bilge and Beste the people committing to git2. `git2ctl restore` (WBS-12.3), which must read the same snapshot format, also lives here. git2-runner made the same move for ci-svc (its ADR 0001).

## Decision

- backup-svc is built in this repo and keeps its name. Its schema is `backup_svc`; its migrations are still written in git2 (`migrations/NNN_backup_<what>.sql`).
- Its gRPC contract is package `backup.v1` in `proto/backup/v1/`. As in git2-codeplane's ADR 0004, this repo is the source of truth, Go code is generated into `gen/go/` and committed, and git2's `contracts/backup/README.md` points to a pinned tag.
- The snapshot format (`docs/api/snapshot-format.md`) is part of the contract: backup-svc writes it and `git2ctl restore` reads it, in one repo, changed in one PR.
- git2 gets no `backup-svc/` directory.

## Consequences

- The writer and the reader of snapshots cannot drift apart.
- git2's README table needs a PR that points `backup-svc` here.
- The all-in-one image runs backup-svc as its own process next to `git2`, as for ci-svc (git2-runner assumption A-08).
- The course reports describe backup-svc as part of git2-deploy from the Requirement Specification on.

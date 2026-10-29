# backup.v1 — backup-svc API (contract v0)

backup-svc takes encrypted snapshots of the whole instance and keeps them in the instance bucket. The `.proto` files in [`proto/backup/v1/`](../../proto/backup/v1/) are the source of truth: every RPC, field and error code is documented there. This page shows who calls what. The implementation starts in W4 (WBS-12.1).

**Status:** v0, open for review by Console (Beste) and admin-api.

## Who uses it

| Caller | Uses |
|---|---|
| Console (Beste), through admin-api | list snapshots, "Back up now", delete, show and edit schedule and retention |
| admin-api's audit log | events `SnapshotFinished`, `SnapshotDeleted`, `BackupSettingsChanged` |
| `git2ctl restore` | no RPC: reads the bucket directly ([snapshot-format.md](snapshot-format.md)) |

backup-svc does no authorization. admin-api checks that the user is an instance admin, then calls with the user's ID in `requested_by_user_id`, `deleted_by_user_id` or `updated_by_user_id`.

## RPCs

| RPC | What it does | Errors to handle |
|---|---|---|
| `CreateSnapshot` | Starts a snapshot, returns it in `STATE_PENDING` right away. Send a fresh UUID in `request_id`; a retry with the same one returns the same snapshot, even while it runs. | `ABORTED`: one is already running, try later. `FAILED_PRECONDITION`: no target configured, retrying won't help |
| `GetSnapshot` | One snapshot by ID. Poll it every few seconds while it runs. | `NOT_FOUND`, `INVALID_ARGUMENT` |
| `ListSnapshots` | Newest first, 50 per page by default, at most 100. | `INVALID_ARGUMENT` for a bad `page_token` |
| `DeleteSnapshot` | Deletes a finished snapshot and its archive. | `FAILED_PRECONDITION` while it runs |
| `GetBackupSettings` | Schedule (cron, UTC), retention, target, encryption recipient. | — |
| `UpdateBackupSettings` | Writes the fields named in `update_mask` (`schedule`, `retention_count`). Target and recipient are read-only; they come from `git2.yaml`. Lowering retention deletes extra snapshots only after the next one succeeds. | `INVALID_ARGUMENT` for an empty mask, a bad cron or a retention outside 1–365 |

Example, from a container on the instance network:

```bash
grpcurl -plaintext -d '{"request_id":"<uuid>","requested_by_user_id":"<admin-id>"}' \
  backup-svc:9091 backup.v1.BackupService/CreateSnapshot
```

```json
{ "snapshot": { "id": "<uuid>", "state": "STATE_PENDING", "trigger": "TRIGGER_MANUAL",
                "requestedByUserId": "<admin-id>", "createdAt": "2026-11-23T10:00:00Z" } }
```

## What the Console shows

- **Snapshot list:** `created_at`, trigger (manual / scheduled), state, `size_bytes`, `repository_count`, and `failure_reason` for failed ones.
- **Settings:** `target.display_name` (for example `s3://acme-git2/backups/`), the schedule, and the retention. Never show or ask for bucket credentials; they live in `git2.yaml`.
- **Restore** is not a button. Show the command an operator runs on the new server: `git2ctl restore <target>/latest`, plus a note that it needs the age private key that matches `recipient`.

## Events

Written to `backup_svc.outbox`, delivered at least once (drop repeats by `event_id`), type = full message name, payload = canonical protobuf JSON. See [`events.proto`](../../proto/backup/v1/events.proto).

| Event | When |
|---|---|
| `backup.v1.SnapshotFinished` | a snapshot succeeded or failed |
| `backup.v1.SnapshotDeleted` | an admin or retention deleted one |
| `backup.v1.BackupSettingsChanged` | schedule or retention changed |

## Not decided yet

- The gRPC port. `9091` above is a placeholder until backup-svc joins Compose (W4).
- Consistent repository copies need help from git-svc ([assumptions.md](../design/assumptions.md), B-01).

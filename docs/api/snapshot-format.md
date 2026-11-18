# Snapshot format (v1)

What backup-svc writes to the bucket and `git2ctl restore` reads. Both live in this repo, so the format changes in one PR. Format version 1, settled in W4 (WBS-12.1). Writer and checker: `internal/snapshot` (`Write`, `Verify`); tests in `tests/snapshot/`.

## In the bucket

backup-svc owns the `backups/` prefix of the instance bucket. Other lanes keep their own prefixes (`ci/` for git2-runner) and never write here.

```
backups/
  20261123T020000Z_<snapshot-id>/
    snapshot.tar.zst.age     the archive: tar, compressed with zstd, encrypted with age
    snapshot.json            plain-text index, no secrets (below)
  latest                     key of the newest succeeded snapshot directory, written last
```

- The directory name starts with the UTC time, so a plain listing is in time order.
- `snapshot.json` and `latest` are written only after the archive upload has finished. A snapshot without `snapshot.json` is incomplete and is never restored.

`snapshot.json`:

```json
{
  "format_version": 1,
  "id": "<snapshot-id>",
  "created_at": "2026-11-23T02:00:00Z",
  "git2_version": "1.0.0",
  "recipient": "age1...",
  "archive": { "key": "snapshot.tar.zst.age", "size_bytes": 123456, "sha256": "<hex>" }
}
```

`git2ctl restore` checks `sha256` before decrypting, so a damaged upload fails early with a clear message.

## Inside the archive

| Path | What | Source |
|---|---|---|
| `manifest.json` | format version, snapshot ID, git2 version, and a size and sha256 for every file below | backup-svc |
| `database/git2.dump` | the whole database, every service schema, `pg_dump --format=custom` | PostgreSQL |
| `repositories/<repo-id>.bundle` | one Git bundle per repository with all refs | git-svc (B-01) |
| `config/git2.yaml` | instance configuration | git2.yaml |
| `secrets/` | instance secrets: auth-svc's JWT key, ci-svc's `GIT2_CI_SECRETS_KEY` (git2-runner A-05), backup-svc's own settings | secret store |

Entries are plain files (PAX tar, mode 0600, no owner names or times from the backed-up server), in this order: the database, the repositories sorted by ID, `git2.yaml`, the secrets sorted by name, and **`manifest.json` last**. An empty repository has an empty `.bundle` file. A repository deleted after it was listed (git-svc answers NOT_FOUND) is left out; any other error fails the whole snapshot.

`manifest.json`:

```json
{
  "format_version": 1,
  "id": "<snapshot-id>",
  "created_at": "2026-11-23T02:00:00Z",
  "git2_version": "1.0.0",
  "files": [
    { "path": "database/git2.dump", "size_bytes": 123, "sha256": "<hex>" }
  ]
}
```

A snapshot is accepted only when (`Verify`): `snapshot.json` exists and its `format_version` is known; the archive has its size and sha256 (before decrypting); it decrypts with the operator's key; every entry is a plain file with a clean relative path (no `..`, no leading `/`) that appears once; `manifest.json` is the last entry, names the same snapshot ID, and lists exactly the other entries with their sizes and checksums. Anything else is `ErrDamaged` and a restore stops before writing.

Not in the archive:
- Redis. It holds nothing durable.
- `ci/cache/` and the rest of the bucket. Restore keeps using the same bucket, or a bucket migration copies it.
- git-svc's `tmp/`, `home/` and `trash/` (git2-codeplane's version note of 26 Oct).

## Order and consistency

1. `pg_dump` runs first, in one repeatable-read transaction, so all schemas are from the same moment.
2. Then the repositories are bundled. A repository created after step 1 is in the archive but has no database row. After a restore, git-svc's sweeper removes it, as ADR 0005 of git2-codeplane already plans. Doing the steps the other way round would leave rows pointing at missing repositories, which is worse.
3. A Git bundle is a consistent copy of one repository even while a push is running. GitLab's backup uses the same per-repository bundles.

## Writing

- `snapshot.json` is written after the archive is complete, and `latest` after that, each to a temporary name, synced and renamed. A failed snapshot removes its directory and leaves `latest` alone.
- tar needs an entry's size before its content, and a dump or a bundle has none in advance. Each entry is first spooled to a file in a private `0700` directory under the target, hashed on the way, copied into the archive and removed. **Disk:** the target needs room for the archive plus the largest single entry.
- **Open:** a crash mid-snapshot can leave that spool directory, which holds plain-text data, and a snapshot directory without `snapshot.json`. backup-svc runs one snapshot at a time, so at startup it removes `.spool-*` and every snapshot directory without `snapshot.json` (next backup-svc PR, #24).

## Encryption

- age ([age-encryption.org/v1](https://age-encryption.org/v1)), X25519 recipient, streamed. The private key never touches the server that is backed up; the operator keeps it.
- Restore without the key is impossible by design. The W9 security check verifies that an archive cannot be read without it.

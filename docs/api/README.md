# Platform & Backup API reference

Contract v0 (week 1). The decisions behind each page are in [decisions/](../decisions/).

| Page | For | Covers |
|---|---|---|
| [`compose.md`](compose.md) | everyone | Running git2 with one `docker compose up`: services, ports, settings, the rules every service image follows |
| [`gateway.md`](gateway.md) | Dash, Console, Codeplane | Routes, sign-in checks, the headers upstreams receive, limits and errors |
| [`backup.md`](backup.md) | Console | `backup.v1`: snapshots, backup settings, events |
| [`snapshot-format.md`](snapshot-format.md) | Codeplane, CI | What a snapshot contains and what restore needs from each service |

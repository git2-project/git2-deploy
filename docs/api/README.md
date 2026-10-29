# Platform & Backup API reference

The week 1 contracts land here by 1 Nov 2026. Until then, the decisions behind them are in [decisions/](../decisions/).

Planned pages:

| Page | For | Covers |
|---|---|---|
| `compose.md` | everyone | Running git2 with one `docker compose up`: services, ports, settings, the rules every service image follows |
| `gateway.md` | Dash, Console, Codeplane | Routes, sign-in checks, the headers upstreams receive, limits and errors |
| `backup.md` | Console | `backup.v1`: snapshots, backup settings, events |
| `snapshot-format.md` | Codeplane, CI | What a snapshot contains and what restore needs from each service |

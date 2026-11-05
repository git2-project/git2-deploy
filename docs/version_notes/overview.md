# Version notes overview

One line per deploy-lane change that touches another lane, **newest first**.
Open the linked note for the details.

| Tag | Meaning |
|---|---|
| 🔴 Breaking | Something your lane already built stops working until you change it. |
| 🟠 Action | Nothing breaks, but your lane must build or change something. |
| 🟢 New | New service, contract, setting or option. Use it when you're ready. |
| ⚪ FYI | Inside the deploy lane only. Nothing to do. |

**Affects** names the lanes with something to read or do: Dash (Bilge),
Console (Beste), Codeplane (Apo), CI (Osman).

Notes live in `<year>/<month>/<day>_<month>.md`.

---

## 2026 · October

| Date | What changed | Affects | Tag |
|---|---|---|---|
| [28 Oct](2026/10/28_10.md) | `docker compose up` runs git2 (Postgres 18, Redis, migrations, git2 core, git-svc). Contracts v0 for `backup.v1` and the gateway are ready for review. Console: review backup.v1. git2: client IP from `X-Real-IP` behind the gateway. Codeplane: a bundle RPC for backups. CI: A-05/06/08/09/15 answered. | All | 🟠 Action |

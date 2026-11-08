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

## 2026 · November

| Date | What changed | Affects | Tag |
|---|---|---|---|
| [6 Nov](2026/11/06_11.md) | Gateway contract v0.1: it authenticates, services authorize. `X-Git2-User-Id` on Git routes only; `/api` services verify the token themselves. API Redis checks fail open as in auth-svc. Git route to `git-svc:9080`, owner-only in v0. Dash: `/raw` headers and size cap. Console: username lookup for Git sign-in. Codeplane: cut `v0.1.0`. Compose: the gateway is the only published port; `GIT2_SUBNET`; git-svc 9080. | All | 🟠 Action |

## 2026 · October

| Date | What changed | Affects | Tag |
|---|---|---|---|
| [28 Oct](2026/10/28_10.md) | `docker compose up` runs git2 (Postgres 18, Redis, migrations, git2 core, git-svc). Contracts v0 for `backup.v1` and the gateway are ready for review. Console: review backup.v1. git2: client IP from `X-Real-IP` behind the gateway. Codeplane: a bundle RPC for backups. CI: A-05/06/08/09/15 answered. | All | 🟠 Action |

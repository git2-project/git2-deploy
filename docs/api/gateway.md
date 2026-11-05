# Gateway (contract v0)

The gateway is the one HTTPS entry point of a git2 instance (WBS-1.2, W2–W3). This page is the contract other lanes build against: which paths go where, what is checked, and what upstreams receive. Where the gateway runs is proposed in [ADR 0004](../decisions/0004-gateway-is-its-own-process.md).

**Status:** v0. Nothing here is built yet. Until W2, `git2` answers directly on `127.0.0.1:8080` ([compose.md](compose.md)).

## Routes

First match wins. The Git route comes first and matches only when the second path segment ends in `.git`, so it cannot be confused with the others.

| Path | Goes to | Sign-in check at the gateway | Limits |
|---|---|---|---|
| `/<owner>/<repo>.git/*` | git-svc Smart HTTP, as `/<repo-id>.git/*` | Git credentials (below) | Git limits |
| `GET /healthz` | gateway itself: is the process up | none | — |
| `GET /readyz` | gateway itself: are git2 core and git-svc reachable | none | — |
| `/api/auth/*` | git2 core (auth-svc) | none; auth-svc checks `/api/auth/me` itself | API limits |
| `/api/*` | git2 core (dash-api, admin-api) | access token required | API limits |
| `/admin`, `/admin/*` | admin-web static files; unknown paths serve `admin/index.html` | none (the app signs in through `/api`) | — |
| `/*` | dash-web static files; unknown paths serve `index.html` | none | — |

**Reserved names.** Because Dash pages use `/<owner>/<repo>` paths, no user or organization may be called `api`, `admin`, `healthz`, `readyz`, `assets`, `static`, `login`, `logout`, `settings`, `explore` or `new`. auth-svc must refuse them at sign-up and rename (assumption B-05).

CI runners do not use the gateway. They connect to ci-svc's runner port directly (git2-runner assumption A-09).

## Sign-in check

- **API:** `Authorization: Bearer <access token>`. The gateway checks the token the same way auth-svc does: HS256, `exp` required, and the JTI is not in Redis as `revoked:<jti>`.
  - A missing, invalid, expired or revoked token gets `401 {"error": "..."}`.
  - A Redis or upstream fault gets `503`, never `401`, because clients drop their tokens on a 401.
- **Git over HTTPS:** HTTP Basic auth, as every Git client sends it.
  - W2–W3: username and password, checked through auth-svc.
  - W6: personal access tokens (WBS-3.2) replace passwords for Git.
  - An anonymous request gets `401` with `WWW-Authenticate: Basic realm="git2"`, so the Git client asks for credentials.
  - If auth-svc, Redis or the database cannot answer, the gateway returns `503`, never `401`.
- **Owner and name to IDs:** the gateway resolves `<owner>/<repo>` to IDs (the owner through auth-svc, then `FindRepository` on git-svc) and forwards by repository ID. git-svc never sees names (git2-codeplane ADR 0002). An unknown repository and one the user may not read both get `404`, so names do not leak.

## What upstreams receive

| Header | Value |
|---|---|
| `X-Git2-User-Id` | the signed-in user's ID; absent on public routes |
| `X-Request-Id` | a request ID, or the client's if it sent a valid one; also returned to the client |
| `X-Real-IP` | the client's IP address, as the gateway saw it |
| `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Forwarded-Host` | set by the gateway; `X-Forwarded-For` is the client IP alone |

The gateway is the edge: it deletes every `X-Git2-*`, `X-Real-IP` and `X-Forwarded-*` header the client sent and writes its own, so a client cannot claim to be someone else or spoof its IP.

Upstreams trust these headers only on connections from the gateway. Two things enforce that:
- once the gateway exists, it is the only published service; git2 core and git-svc have no host port;
- git2 core accepts `X-Real-IP` only when the connection comes from the gateway's network address.

Today git2 core reads the client IP from `RemoteAddr` (`internal/ratelimit`), and its comment expects the gateway to rewrite that. A separate gateway cannot do this, so git2 core must switch to `X-Real-IP` as above (assumption B-02, [version note, 28 Oct](../version_notes/2026/10/28_10.md)).

## Limits

| | API (`/api/*`) | Git (`*.git/*`) |
|---|---|---|
| Request body | 64 KiB, enforced by git2 core | no cap at the gateway; push size limits come later |
| Timeout | 30 s per request | no total timeout; 5 min idle |
| Rate limit | per user and per IP (WBS-1.4, W5) | failed sign-ins per IP and per username from W2–W3, with the Smart HTTP route; per user from W5 |

## Errors

Every error from the gateway itself is JSON, `{"error": "<message>"}`, like git2 core's. Git routes answer `401` and `404` in plain text, because Git clients print the body.

| Status | When |
|---|---|
| 401 | sign-in missing or invalid |
| 404 | no route, or a repository the user may not see |
| 413 | body over the limit |
| 429 | rate limit (W5), with `Retry-After` |
| 502 | upstream answered with a broken response |
| 503 | upstream or Redis not reachable |

## TLS

The gateway terminates TLS: a certificate and key from `git2.yaml`, or ACME (Let's Encrypt) when the instance has a public name. Plain HTTP on port 80 only redirects to HTTPS. Details are settled with `git2ctl install` (W3–W4).

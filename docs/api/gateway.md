# Gateway (contract v0.1)

The gateway is the one HTTPS entry point of a git2 instance (WBS-1.2, W2–W3). This page is the contract other lanes build against: which paths go where, what is checked, and what upstreams receive. Where the gateway runs is proposed in [ADR 0004](../decisions/0004-gateway-is-its-own-process.md).

**Status:** v0.1 (6 Nov), aligned with auth-svc (git2#14, git2#18), git-svc Smart HTTP (git2-codeplane#5) and the dash-api contract (git2#17). Nothing here is built yet. Until it is, `git2` answers directly on `127.0.0.1:8080` ([compose.md](compose.md)).

## Routes

First match wins. The Git route comes first and matches only when the second path segment ends in `.git`, so it cannot be confused with the others.

| Path | Goes to | Sign-in check at the gateway | Limits |
|---|---|---|---|
| `/<owner>/<repo>.git/*` | git-svc Smart HTTP (`git-svc:9080`), as `/<repo-id>.git/*` | Git credentials (below) | Git limits |
| `GET /healthz` | gateway itself: is the process up | none | none |
| `GET /readyz` | gateway itself: are git2 core and git-svc reachable | none | none |
| `/api/auth/*` | git2 core (auth-svc) | none; auth-svc checks `/api/auth/me` itself | API limits |
| `/api/setup/*` | git2 core (auth-svc first-admin setup, git2#18) | none; auth-svc checks the one-time setup token | API limits |
| `/api/*` | git2 core (dash-api, admin-api) | access token required | API limits |
| `/admin`, `/admin/*` | admin-web static files; unknown paths serve `admin/index.html` | none (the app signs in through `/api`) | none |
| `/*` | dash-web static files; unknown paths serve `index.html` | none | none |

**Reserved names.** Because Dash pages use `/<owner>/<repo>` paths, no user or organization may be called `api`, `admin`, `healthz`, `readyz`, `assets`, `static`, `login`, `logout`, `settings`, `explore` or `new`. auth-svc must refuse them at sign-up and rename (assumption B-05).

CI runners do not use the gateway. They connect to ci-svc's runner port directly (git2-runner assumption A-09).

## Sign-in check

The gateway **authenticates; it does not authorize.** It decides who the caller is. Whether that caller may read or change a resource is decided by the service that owns it. A resource the caller may not see answers `404`, not `403`, so names do not leak.

### API (`/api/*`)

- `Authorization: Bearer <access token>`. The gateway runs auth-svc's own check (`authsvc.RequireAuth`): HS256, `exp` required, the JTI is not revoked (`revoked:<jti>` in Redis), and the token was not issued before its user was disabled (`disabled:<uid>` in Redis).
- A missing, invalid, expired or revoked token, or a disabled user, gets `401 {"error": "..."}` and never reaches git2 core.
- **Redis fault:** the revoked and disabled checks fail open, as in auth-svc (agreed in git2#14). The token's signature and expiry are still checked, so the worst case is a revoked token that works until it expires (`JWT_EXPIRY`, 15 min). The gateway logs each such request at WARN.
- The gateway forwards `Authorization` unchanged. **Services verify the token again** with `authsvc.RequireAuth` and read the caller with `authsvc.UserID(ctx)`. They do not read `X-Git2-User-Id`, which the gateway does not set on `/api/*`.

### Git over HTTPS (`*.git/*`)

- HTTP Basic auth, as every Git client sends it. The username is the git2 **username**, the password the account password.
  - W2–W3: the gateway checks them against auth-svc's users (bcrypt) and refuses inactive accounts and accounts with a `disabled:<uid>` marker.
  - W6: personal access tokens (WBS-3.2) replace passwords for Git.
- An anonymous request, a wrong password or a disabled account gets `401` with `WWW-Authenticate: Basic realm="git2"`, so the Git client asks for credentials.
- If the database or Redis cannot answer, the gateway returns `503`, never `401`.
- **Who may use a repository (v0):** only its owner. `<owner>` must be the signed-in user's username; the gateway then calls `FindRepository(owner_id, name)` on git-svc and forwards by repository ID. git-svc never sees names (git2-codeplane ADR 0002). Any other owner, and an unknown repository, get `404`. Repository roles, teams and visibility replace this rule when they exist (dash-api contract, open questions 3 and 4).

## What upstreams receive

| Header | Routes | Value |
|---|---|---|
| `Authorization` | `/api/*` | the client's, unchanged |
| `X-Git2-User-Id` | Git only | the signed-in user's ID |
| `X-Request-Id` | all | a request ID, or the client's if it sent a valid one; also returned to the client |
| `X-Real-IP` | all | the client's IP address, as the gateway saw it |
| `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Forwarded-Host` | all | set by the gateway; `X-Forwarded-For` is the client IP alone |
| `Content-Type`, `Content-Encoding`, `Git-Protocol` | Git | the client's, unchanged; bodies stream both ways without buffering |

The gateway is the edge: **on every route** it deletes every `X-Git2-*`, `X-Real-IP` and `X-Forwarded-*` header the client sent and writes its own, so a client cannot claim to be someone else or spoof its IP.

Upstreams trust these headers only on connections from the gateway. Two things enforce that:
- once the gateway exists, it is the only published service; git2 core and git-svc have no host port;
- git2 core accepts `X-Real-IP` only when the connection comes from the gateway's network address.

Today git2 core reads the client IP from `RemoteAddr` (`internal/ratelimit`), and its comment expects the gateway to rewrite that. A separate gateway cannot do this, so git2 core must switch to `X-Real-IP` as above (assumption B-02, [version note, 28 Oct](../version_notes/2026/10/28_10.md)).

## What clients receive

- Response headers from upstreams pass through unchanged (`Cache-Control`, `Content-Disposition`, `Content-Security-Policy` and the rest). A service sets its own; for example dash-api sets them on `/raw`.
- The gateway adds `X-Content-Type-Options: nosniff` to every response.

## Limits

| | API (`/api/*`) | Git (`*.git/*`) |
|---|---|---|
| Request body | 64 KiB, enforced by git2 core | no cap at the gateway; git-svc caps a push at 2 GiB and a fetch request at 64 MiB |
| Response body | no cap at the gateway; a service caps its own (for example `/raw` in dash-api) | no cap |
| Timeout | 30 s per request | no total timeout at the gateway, 5 min idle; git-svc ends one operation after 2 h |
| Rate limit | per user and per IP (WBS-1.4, W5) | failed sign-ins per IP and per username from W2–W3, with the Git route; per user from W5 |

## Errors

Every error from the gateway itself is JSON, `{"error": "<message>"}`, like git2 core's. Git routes answer `401`, `404` and `503` in plain text, because Git clients print the body.

| Status | When |
|---|---|
| 401 | sign-in missing or invalid |
| 404 | no route, or a repository the user may not see |
| 413 | body over the limit |
| 429 | rate limit (W5), with `Retry-After` |
| 502 | upstream answered with a broken response |
| 503 | upstream, the database or Redis not reachable (except the fail-open API checks above) |

## TLS

The gateway terminates TLS: a certificate and key from `git2.yaml`, or ACME (Let's Encrypt) when the instance has a public name. Plain HTTP on port 80 only redirects to HTTPS. Details are settled with `git2ctl install` (W3–W4).

## Open questions

1. **Download links.** `<a href>` and `<img src>` cannot send the bearer header, so Dash fetches `/raw` and opens a Blob URL (dash-api contract, open question 6). Later: short-lived signed URLs (`?exp=…&sig=…`, HMAC). Who verifies them, the gateway or dash-api, is open.

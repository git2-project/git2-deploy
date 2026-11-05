# 0004. The gateway is its own process (proposed)

- Status: Proposed, for the W2 sprint planning
- Date: 2026-10-28

## Context

The gateway (WBS-1.2, W2–W3) is the one HTTPS entry point: TLS, routing, the sign-in check, and serving both web apps. Behind it sit git2 core (every `/api` route, one binary) and git-svc's Smart HTTP (`git clone` / `git push`).

The two kinds of traffic need different limits:

- The API answers small JSON requests. git2 core caps bodies at 64 KiB and cancels requests after 30 s (`middleware.Timeout`).
- Git over HTTPS streams packfiles that can be hundreds of MB and take minutes. A 30 s timeout or a body cap would break large clones and pushes.

How others solve it:
- **GitLab** puts **Workhorse**, a small Go reverse proxy, in front of Rails. It handles Git over HTTP, uploads and long requests itself and passes only short requests on ([GitLab Workhorse docs](https://docs.gitlab.com/development/workhorse/)).
- **Gitea and Forgejo** serve everything from one binary, with no per-route split of timeouts.

## Decision (proposed)

- The gateway is its own Go process and image (`gateway`), in the git2 repo under `gateway/`, owned by the Platform lane.
  - It reuses `authsvc.RequireAuth` and `internal/` from that module instead of copying them.
- It applies per-route limits: short timeouts and the body cap on `/api`, and streaming with idle timeouts only on Git routes.
- It strips client-sent `X-Git2-*` headers and sets `X-Git2-User-Id` and `X-Real-IP` for upstreams (see [docs/api/gateway.md](../api/gateway.md)).
- In Compose and Helm it is the only service with a public port.

## Consequences

- Smart HTTP traffic never passes through git2 core's API timeouts.
- One more image and one more hop, under 1 ms on a local network.
- git2 core must take the client IP from `X-Real-IP`, but only when the request comes from the gateway. Today `internal/ratelimit` reads `RemoteAddr`, so behind a separate gateway every client would share one rate-limit bucket. An action item for the git2 owners is in the version note of 28 Oct.

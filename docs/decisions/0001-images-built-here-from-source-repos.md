# 0001. Image recipes live here and build from the source repos

- Status: Accepted
- Date: 2026-10-28

## Context

WBS-13.1 asks for one Docker image per service and a Compose file that starts them with PostgreSQL and Redis. The services live in three repos (git2, git2-codeplane, git2-runner), and each repo's rules say other lanes never edit it. The team map gives the deploy lane "deployment for every module". Images must build for amd64 and arm64 (D-06).

Two common layouts:

- **Dockerfile next to the code** (Gitea, Sourcegraph). Each owner keeps an image recipe in their repo, and the deploy repo only references image names.
- **A separate images repo** (GitLab's [CNG](https://gitlab.com/gitlab-org/build/CNG), used by its Helm chart). One repo holds every component's Dockerfile and builds from the component's source at a ref.

## Decision

- Every image recipe lives here, in `images/<name>/Dockerfile`, like GitLab CNG.
- The source comes in as a BuildKit **named context** called `src`, set by Compose's `build.additional_contexts`. Its value is a local checkout (default: the sibling directory, `../../git2`) or a Git URL with a ref (`https://github.com/git2-project/git2.git#main`). All four repos are public, so Git URLs need no credentials.
- Each image also has a fixed name, `ghcr.io/git2-project/<name>:${GIT2_TAG}`, so once the image pipeline (WBS-13.4) publishes, `docker compose pull` uses the published image and skips the build.
- Go images cross-compile (`FROM --platform=$BUILDPLATFORM`, then `GOOS/GOARCH` from `TARGETOS/TARGETARCH`), so the arm64 image is not built under emulation.

## Consequences

- Owners of the other repos never have to learn Docker or touch packaging. A change to how a service builds (a new `cmd/` path, a CGO dependency) needs a PR here.
- The Dockerfile and the source can drift: a renamed `cmd/` breaks the build here, not in the source repo. The smoke test (`tests/smoke/compose_smoke.sh`) catches that, and builds against a source branch with `GIT2_SRC=<url>#<branch>`.
- Developers get a full stack from their sibling checkouts with no registry and no push rights.
- Measured on 2026-10-28 (Apple M-series, Docker 29.7): git2 image 23.4 MB (distroless static), git-svc 55.6 MB (Alpine + git), migrate adds a few kB on top of `postgres:18-alpine`, which the stack pulls anyway. A cold build of all three takes about 20 s, and a multi-arch (amd64 + arm64) build of git2 takes under 10 s once the module cache is warm.

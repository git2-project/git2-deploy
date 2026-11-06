# syntax=docker/dockerfile:1
#
# gateway: git2's one entry point (cmd/gateway in the git2 repo) with the
# built Dash and Admin web apps. Contract: docs/api/gateway.md.
#
# The source comes from the named build context "src", a git2 checkout (see
# compose/compose.yaml). COPY --exclude (Dockerfile 1.19+) keeps a local
# node_modules out: it holds binaries for the host, not for Linux.

ARG GO_VERSION=1.26
ARG NODE_VERSION=22

# ── Web apps: platform-independent files, so build once on the host's platform.
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS web
WORKDIR /web
# The pnpm version comes from web/package.json ("packageManager").
RUN corepack enable
COPY --from=src --exclude=**/node_modules --exclude=**/dist web/ ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile \
    && pnpm build

# ── Go binary, cross-compiled like the git2 image.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY --from=src go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY --from=src --exclude=web . .
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

# Static binary and static files, no shell, runs as uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/git2-project/git2" \
      org.opencontainers.image.description="git2 gateway with the Dash and Admin web apps"
COPY --from=build /out/gateway /usr/local/bin/gateway
COPY --from=web /web/apps/dash/dist /srv/web/dash
COPY --from=web /web/apps/admin/dist /srv/web/admin
ENV GATEWAY_PORT=8000 \
    GATEWAY_WEB_DIR=/srv/web
USER nonroot:nonroot
EXPOSE 8000
ENTRYPOINT ["/usr/local/bin/gateway"]

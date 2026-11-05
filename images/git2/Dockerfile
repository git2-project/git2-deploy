# syntax=docker/dockerfile:1
#
# git2 core: the single binary from the git2 repo (cmd/git2) that mounts the
# control-plane services under /api. Today that is auth-svc.
#
# The source comes from the named build context "src", which Compose sets to a
# git2 checkout (a local path or a Git URL, see compose/compose.yaml). This
# directory holds only the Dockerfile, so the image recipe lives in the deploy
# lane and the source stays in its own repo.

ARG GO_VERSION=1.26

# Build on the host's platform and cross-compile, so an arm64 image does not
# build under emulation.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY --from=src go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY --from=src . .
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/git2 ./cmd/git2

# Static binary, no shell, no package manager, runs as uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/git2-project/git2" \
      org.opencontainers.image.description="git2 core services (auth-svc)"
COPY --from=build /out/git2 /usr/local/bin/git2
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/git2"]

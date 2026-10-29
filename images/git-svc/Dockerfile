# syntax=docker/dockerfile:1
#
# git-svc: the Git engine from the git2-codeplane repo. The source comes from
# the named build context "src" (see compose/compose.yaml).
#
# Unlike git2 core it needs the git binary at run time (2.38 or newer, for
# merge-tree --write-tree), so the runtime image is Alpine with git instead of
# distroless.

ARG GO_VERSION=1.26

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY --from=src go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY --from=src . .
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/git-svc ./git-svc/cmd/git-svc

FROM alpine:3.22
LABEL org.opencontainers.image.source="https://github.com/git2-project/git2-codeplane" \
      org.opencontainers.image.description="git2 git-svc (Git engine)"
RUN apk add --no-cache git ca-certificates \
    && addgroup -S -g 10001 git2 \
    && adduser -S -D -H -u 10001 -G git2 git2 \
    && install -d -o git2 -g git2 -m 0750 /var/lib/git2
COPY --from=build /out/git-svc /usr/local/bin/git-svc
ENV GIT2_STORAGE_ROOT=/var/lib/git2 \
    GIT2_GITSVC_GRPC_ADDR=:9090
USER git2:git2
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/git-svc"]
CMD ["serve"]

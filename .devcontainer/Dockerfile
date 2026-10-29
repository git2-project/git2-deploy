FROM golang:1.26-bookworm

# curl and jq for the smoke test, psql and redis-cli to look inside the stack.
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    make \
    curl \
    jq \
    ca-certificates \
    postgresql-client \
    redis-tools \
    && rm -rf /var/lib/apt/lists/*

# Docker CLI with the Compose and Buildx plugins. The daemon is the `docker`
# compose service.
COPY --from=docker:29-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=docker:29-cli /usr/local/libexec/docker/cli-plugins /usr/local/libexec/docker/cli-plugins

# Pinned: gopls v0.24+ needs Go 1.27. Bump with the base image.
RUN go install golang.org/x/tools/gopls@v0.23.0 \
    && go install golang.org/x/tools/cmd/goimports@v0.52.0

WORKDIR /src/git2-deploy

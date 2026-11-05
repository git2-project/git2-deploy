#!/usr/bin/env bash
#
# compose_smoke.sh — bring up compose/compose.yaml in a throwaway project and
# check every promise the Compose stack makes (docs/design/compose.md):
#
#   1. It refuses to start without its secrets.
#   2. `up` starts PostgreSQL, Redis and git2; migrations run first, once.
#   3. Sign-in works end to end: register, log in, call /me with the token,
#      and a wrong password is rejected.
#   4. PostgreSQL and Redis are not published on the host; git2 only on
#      127.0.0.1.
#   5. Every git2 container (git2, migrate, git-svc) runs as a non-root user
#      on a read-only file system with no capabilities.
#   6. Data survives `down` + `up`: migrations are not re-applied and the
#      user can still log in.
#   7. With --with-git-svc: git-svc starts and creates a repository over gRPC.
#
# Usage: tests/smoke/compose_smoke.sh [--with-git-svc] [--keep]
# Env:   GIT2_SRC, CODEPLANE_SRC (default: sibling checkouts), SMOKE_PORT.
# Needs: docker with compose v2, curl, openssl.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_DIR="$ROOT/compose"
PROJECT="git2-smoke-$$"
PORT="${SMOKE_PORT:-18080}"
BASE="http://127.0.0.1:$PORT"
WITH_GIT_SVC=0
KEEP=0
for arg in "$@"; do
	case "$arg" in
	--with-git-svc) WITH_GIT_SVC=1 ;;
	--keep) KEEP=1 ;;
	*) echo "unknown argument: $arg" >&2; exit 2 ;;
	esac
done

PROFILE_ARGS=()
[ "$WITH_GIT_SVC" -eq 1 ] && PROFILE_ARGS=(--profile git-svc)

# The run gets its own env file, so a developer's compose/.env (for example
# GIT2_BIND=0.0.0.0) never leaks into it. Only the source paths are passed
# through.
TMP="$(mktemp -d)"
ENV_FILE="$TMP/env"
umask 077
{
	echo "POSTGRES_PASSWORD=$(openssl rand -hex 24)"
	echo "JWT_SECRET=$(openssl rand -hex 32)"
	echo "GIT2_HTTP_PORT=$PORT"
	echo "GIT2_TAG=smoke"
	echo "GIT2_SRC=${GIT2_SRC:-../../git2}"
	echo "CODEPLANE_SRC=${CODEPLANE_SRC:-../../git2-codeplane}"
} >"$ENV_FILE"
unset GIT2_BIND POSTGRES_PASSWORD JWT_SECRET

compose() { docker compose --project-directory "$COMPOSE_DIR" -p "$PROJECT" "$@"; }
dc() { compose --env-file "$ENV_FILE" ${PROFILE_ARGS[@]+"${PROFILE_ARGS[@]}"} "$@"; }

cleanup() {
	if [ "$KEEP" -eq 1 ]; then
		echo "kept project $PROJECT (docker compose -p $PROJECT down -v)"
		return
	fi
	dc down -v --remove-orphans >/dev/null 2>&1 || true
}

pass=0
ok() { pass=$((pass + 1)); echo "  ok   $*"; }
fail() {
	echo "  FAIL $*" >&2
	dc ps -a >&2 || true
	dc logs --tail 30 >&2 || true
	exit 1
}

wait_healthz() {
	for _ in $(seq 1 60); do
		curl -fsS "$BASE/healthz" >/dev/null 2>&1 && return 0
		sleep 1
	done
	return 1
}

# status METHOD PATH [BODY] [TOKEN] -> prints the HTTP status, keeps headers
# in $TMP/headers and the body in $TMP/body.
trap 'cleanup; rm -rf "$TMP"' EXIT
status() {
	local args=(-sS -o "$TMP/body" -D "$TMP/headers" -w '%{http_code}' -X "$1" "$BASE$2")
	[ -n "${3:-}" ] && args+=(-H 'Content-Type: application/json' --data "$3")
	[ -n "${4:-}" ] && args+=(-H "Authorization: Bearer $4")
	curl "${args[@]}"
}

echo "== 1. secrets are required"
# Each secret is blanked in a copy of the env file. The refusal must name
# that secret; any other error (no Docker, bad YAML) fails the check.
for secret in POSTGRES_PASSWORD JWT_SECRET; do
	sed "s/^$secret=.*/$secret=/" "$ENV_FILE" >"$TMP/env.blank"
	if out="$(compose --env-file "$TMP/env.blank" config -q 2>&1)"; then
		fail "compose accepted an empty $secret"
	fi
	echo "$out" | grep -q "set $secret in .env" || fail "compose failed for another reason: $out"
done
compose --env-file "$ENV_FILE" config -q || fail "compose rejects the complete env file"
ok "config refuses empty POSTGRES_PASSWORD and JWT_SECRET"

echo "== 2. up"
dc up -d --build --quiet-pull >"$TMP/up.log" 2>&1 || fail "docker compose up: $(tail -20 "$TMP/up.log")"
wait_healthz || fail "git2 /healthz did not answer within 60s"
[ "$(dc ps -a --format '{{.State}} {{.ExitCode}}' migrate)" = "exited 0" ] || fail "migrate did not exit 0"
dc logs migrate | grep -q 'applied [0-9]* migration' || fail "migrate applied nothing"
ok "postgres, redis and git2 up; migrate ran and exited 0"

echo "== 3. sign-in"
user="smoke$RANDOM"
body="{\"email\":\"$user@example.test\",\"username\":\"$user\",\"password\":\"Smoke-test-pw-1\"}"
[ "$(status POST /api/auth/register "$body")" = 201 ] || fail "register: $(cat "$TMP/body")"
login="{\"email\":\"$user@example.test\",\"password\":\"Smoke-test-pw-1\"}"
[ "$(status POST /api/auth/login "$login")" = 200 ] || fail "login: $(cat "$TMP/body")"
token="$(sed -n 's/^[Xx]-[Aa]ccess-[Tt]oken: *//p' "$TMP/headers" | tr -d '\r')"
[ -n "$token" ] || fail "login returned no X-Access-Token header"
[ "$(status GET /api/auth/me '' "$token")" = 200 ] || fail "me: $(cat "$TMP/body")"
grep -q "\"$user\"" "$TMP/body" || fail "me returned another user"
wrong="{\"email\":\"$user@example.test\",\"password\":\"wrong-password-1\"}"
[ "$(status POST /api/auth/login "$wrong")" = 401 ] || fail "a wrong password was not rejected with 401"
[ "$(status GET /api/auth/me)" = 401 ] || fail "/me without a token was not 401"
ok "register 201, login 200 with token, me 200, wrong password 401, no token 401"

echo "== 4. not published on the host"
for svc in postgres redis; do
	cid="$(dc ps -q "$svc")"
	[ -n "$cid" ] || fail "$svc has no container"
	ports="$(docker inspect -f '{{json .NetworkSettings.Ports}}' "$cid")" || fail "inspect $svc"
	if echo "$ports" | grep -q HostPort; then
		fail "$svc is published on the host: $ports"
	fi
done
cid="$(dc ps -q git2)"
[ -n "$cid" ] || fail "git2 has no container"
binds="$(docker inspect -f '{{range $p, $b := .NetworkSettings.Ports}}{{range $b}}{{.HostIp}} {{end}}{{end}}' "$cid")"
[ "$binds" = "127.0.0.1 " ] || fail "git2 is bound to [$binds], expected only 127.0.0.1"
ok "postgres and redis have no host ports; git2 only on 127.0.0.1"

echo "== 5. hardening"
hardened=(git2 migrate)
[ "$WITH_GIT_SVC" -eq 1 ] && hardened+=(git-svc)
for svc in "${hardened[@]}"; do
	cid="$(dc ps -a -q "$svc")"
	[ -n "$cid" ] || fail "$svc has no container"
	runas="$(docker inspect -f '{{.Config.User}}' "$cid")"
	case "$runas" in "" | root | 0 | root:* | 0:*) fail "$svc runs as root ($runas)" ;; esac
	[ "$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$cid")" = "true" ] || fail "$svc root fs is writable"
	[ "$(docker inspect -f '{{.HostConfig.CapDrop}}' "$cid")" = "[ALL]" ] || fail "$svc keeps capabilities"
	docker inspect -f '{{.HostConfig.SecurityOpt}}' "$cid" | grep -q 'no-new-privileges' || fail "$svc allows new privileges"
done
ok "${hardened[*]}: non-root, read-only, no capabilities, no-new-privileges"

echo "== 6. data survives down + up"
dc down >/dev/null 2>&1 || fail "docker compose down"
dc up -d >/dev/null 2>&1 || fail "docker compose up (second time)"
wait_healthz || fail "git2 did not come back"
dc logs migrate | grep -q 'no pending migrations' || fail "migrations were applied again"
[ "$(status POST /api/auth/login "$login")" = 200 ] || fail "login after restart: $(cat "$TMP/body")"
ok "no pending migrations on restart; the user still logs in"

if [ "$WITH_GIT_SVC" -eq 1 ]; then
	echo "== 7. git-svc"
	[ "$(dc ps --format '{{.State}}' git-svc)" = "running" ] || fail "git-svc is not running"
	src="${CODEPLANE_SRC:-$ROOT/../git2-codeplane}"
	[ -d "$src/proto" ] || fail "--with-git-svc needs CODEPLANE_SRC to be a local checkout with proto/"
	out="$(docker run --rm --network "${PROJECT}_default" -v "$src/proto:/proto:ro" \
		fullstorydev/grpcurl:v1.9.3 -plaintext -import-path /proto -proto git/v1/repository.proto \
		-d '{"owner_id":"0199a0b2-0000-4000-8000-000000000001","name":"smoke"}' \
		git-svc:9090 git.v1.RepositoryService/CreateRepository 2>&1)" || fail "CreateRepository: $out"
	echo "$out" | grep -q '"name": "smoke"' || fail "CreateRepository: $out"
	ok "git-svc created a repository over gRPC"
fi

echo "PASS ($pass checks)"

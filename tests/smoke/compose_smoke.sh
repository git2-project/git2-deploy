#!/usr/bin/env bash
#
# compose_smoke.sh — bring up compose/compose.yaml in a throwaway project and
# check every promise the Compose stack makes (docs/design/compose.md):
#
#   1. It refuses to start without its secrets.
#   2. `up` starts PostgreSQL, Redis, git2 and the gateway; migrations run
#      first, once.
#   3. Sign-in works end to end through the gateway: register, log in, call
#      /me with the token, and a wrong password is rejected.
#   4. Only the gateway is published, on 127.0.0.1. PostgreSQL, Redis and
#      git2 core have no host port.
#   5. Every git2 container (gateway, git2, migrate, git-svc) runs as a
#      non-root user on a read-only file system with no capabilities.
#   6. Data survives `down` + `up`: migrations are not re-applied and the
#      user can still log in.
#   7. The gateway (docs/api/gateway.md): serves Dash and Admin, refuses an
#      API call without a token before git2 core sees it, and git2 core sees
#      the client's IP, not the gateway's or a spoofed one (B-02).
#   8. With --with-git-svc: git-svc starts, creates a repository over gRPC,
#      and its Smart HTTP port (9080) is healthy and not published.
#   9. With --with-git-svc, Git over HTTPS through the gateway: the owner
#      pushes and clones with the real git client; no or wrong credentials
#      get 401 with a Basic challenge; another owner's path is 404.
#  10. With --with-tls (compose.tls.yaml, a throwaway self-signed
#      certificate): HTTPS with HSTS, TLS 1.1 refused, plain HTTP 308 to
#      GIT2_EXTERNAL_URL with path and query; with --with-git-svc, git
#      lists the repository over HTTPS.
#
# Usage: tests/smoke/compose_smoke.sh [--with-git-svc] [--with-tls] [--keep]
# Env:   GIT2_SRC, CODEPLANE_SRC (default: sibling checkouts), SMOKE_PORT.
# Needs: docker with compose v2, curl, openssl, python3; git for checks 9-10.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_DIR="$ROOT/compose"
PROJECT="git2-smoke-$$"
PORT="${SMOKE_PORT:-18080}"
BASE="http://127.0.0.1:$PORT"
WITH_GIT_SVC=0
WITH_TLS=0
KEEP=0
for arg in "$@"; do
	case "$arg" in
	--with-git-svc) WITH_GIT_SVC=1 ;;
	--with-tls) WITH_TLS=1 ;;
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

# wait_ready waits until the gateway answers and git2 core behind it does:
# /api/auth/me without a token is 401 from core, 503 while core is down.
wait_ready() {
	for _ in $(seq 1 90); do
		code="$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/api/auth/me" 2>/dev/null || true)"
		[ "$code" = 401 ] && return 0
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
wait_ready || fail "git2 did not answer through the gateway within 90s"
[ "$(dc ps -a --format '{{.State}} {{.ExitCode}}' migrate)" = "exited 0" ] || fail "migrate did not exit 0"
dc logs migrate | grep -q 'applied [0-9]* migration' || fail "migrate applied nothing"
ok "postgres, redis, git2 and gateway up; migrate ran and exited 0"

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
uid="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <"$TMP/body")"
[ -n "$uid" ] || fail "me returned no user id: $(cat "$TMP/body")"
wrong="{\"email\":\"$user@example.test\",\"password\":\"wrong-password-1\"}"
[ "$(status POST /api/auth/login "$wrong")" = 401 ] || fail "a wrong password was not rejected with 401"
[ "$(status GET /api/auth/me)" = 401 ] || fail "/me without a token was not 401"
ok "register 201, login 200 with token, me 200, wrong password 401, no token 401"

echo "== 4. not published on the host"
unpublished=(postgres redis git2)
[ "$WITH_GIT_SVC" -eq 1 ] && unpublished+=(git-svc)
for svc in "${unpublished[@]}"; do
	cid="$(dc ps -q "$svc")"
	[ -n "$cid" ] || fail "$svc has no container"
	ports="$(docker inspect -f '{{json .NetworkSettings.Ports}}' "$cid")" || fail "inspect $svc"
	if echo "$ports" | grep -q HostPort; then
		fail "$svc is published on the host: $ports"
	fi
done
cid="$(dc ps -q gateway)"
[ -n "$cid" ] || fail "gateway has no container"
binds="$(docker inspect -f '{{range $p, $b := .NetworkSettings.Ports}}{{range $b}}{{.HostIp}} {{end}}{{end}}' "$cid")"
[ "$binds" = "127.0.0.1 " ] || fail "gateway is bound to [$binds], expected only 127.0.0.1"
ok "${unpublished[*]} have no host ports; the gateway only on 127.0.0.1"

echo "== 5. hardening"
hardened=(gateway git2 migrate)
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
wait_ready || fail "git2 did not come back"
dc logs migrate | grep -q 'no pending migrations' || fail "migrations were applied again"
[ "$(status POST /api/auth/login "$login")" = 200 ] || fail "login after restart: $(cat "$TMP/body")"
ok "no pending migrations on restart; the user still logs in"

echo "== 7. gateway"
[ "$(curl -sS -o /dev/null -w '%{http_code} %{content_type}' "$BASE/")" = "200 text/html; charset=utf-8" ] || fail "Dash index not served at /"
[ "$(curl -sS -o /dev/null -w '%{http_code} %{content_type}' "$BASE/admin/users")" = "200 text/html; charset=utf-8" ] || fail "Admin index not served at /admin/users"
asset="$(curl -sS "$BASE/admin/" | grep -o '/admin/assets/[^"]*\.js' | head -1)"
[ -n "$asset" ] && [ "$(curl -sS -o /dev/null -w '%{http_code}' "$BASE$asset")" = 200 ] || fail "Admin asset [$asset] not served"
# Each request carries an ID that git2 core logs; core's log must not have
# the unauthenticated one and must show the client, not the gateway, for the other.
noauth="smoke-noauth-$$"
[ "$(curl -sS -o /dev/null -w '%{http_code}' -H "X-Request-Id: $noauth" "$BASE/api/repos")" = 401 ] || fail "/api/repos without a token was not 401"
spoof="smoke-ip-$$"
curl -sS -o /dev/null -H "X-Request-Id: $spoof" -H 'X-Real-IP: 198.51.100.99' "$BASE/api/auth/me"
sleep 1
corelog="$(dc logs --no-log-prefix git2)"
echo "$corelog" | grep -q "\[$noauth\]" && fail "git2 core received the request without a token"
seen="$(echo "$corelog" | grep "\[$spoof\]" | sed -n 's/.* from \([^ ]*\) - .*/\1/p' | head -1)"
seen="${seen%:*}"
gwip="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$(dc ps -q gateway)")"
[ -n "$seen" ] || fail "git2 core did not log the request [$spoof]"
[ "$seen" != "$gwip" ] || fail "git2 core sees the gateway ($gwip) as the client (B-02)"
[ "$seen" != "198.51.100.99" ] || fail "git2 core believed a client-sent X-Real-IP"
ok "Dash and Admin served; no token is 401 before core; core sees the client ($seen), not the gateway ($gwip) or a spoof"

if [ "$WITH_GIT_SVC" -eq 1 ]; then
	echo "== 8. git-svc"
	[ "$(dc ps --format '{{.State}}' git-svc)" = "running" ] || fail "git-svc is not running"
	src="${CODEPLANE_SRC:-$ROOT/../git2-codeplane}"
	[ -d "$src/proto" ] || fail "--with-git-svc needs CODEPLANE_SRC to be a local checkout with proto/"
	# Owned by the smoke user, so check 9 can push to it as that user.
	out="$(docker run --rm --network "${PROJECT}_default" -v "$src/proto:/proto:ro" \
		fullstorydev/grpcurl:v1.9.3 -plaintext -import-path /proto -proto git/v1/repository.proto \
		-d "{\"owner_id\":\"$uid\",\"name\":\"smoke\"}" \
		git-svc:9090 git.v1.RepositoryService/CreateRepository 2>&1)" || fail "CreateRepository: $out"
	echo "$out" | grep -q '"name": "smoke"' || fail "CreateRepository: $out"
	ok "git-svc created a repository over gRPC"
	health=""
	for _ in $(seq 1 60); do
		health="$(docker inspect -f '{{.State.Health.Status}}' "$(dc ps -q git-svc)")"
		[ "$health" = healthy ] && break
		sleep 1
	done
	[ "$health" = healthy ] || fail "git-svc Smart HTTP (9080) health check: $health (needs git2-codeplane#5)"
	ok "git-svc Smart HTTP port 9080 is healthy"

	echo "== 9. Git over HTTPS through the gateway"
	command -v git >/dev/null || fail "check 9 needs git"
	pw="Smoke-test-pw-1"
	refs="/$user/smoke.git/info/refs?service=git-upload-pack"
	code="$(curl -sS -o /dev/null -D "$TMP/headers" -w '%{http_code}' "$BASE$refs")"
	[ "$code" = 401 ] || fail "Git without credentials: $code, want 401"
	grep -qi '^www-authenticate: basic realm="git2"' "$TMP/headers" || fail "Git 401 without a Basic challenge"
	[ "$(curl -sS -o /dev/null -w '%{http_code}' -u "$user:wrong-password-1" "$BASE$refs")" = 401 ] || fail "Git with a wrong password was not 401"
	[ "$(curl -sS -o /dev/null -w '%{http_code}' -u "$user:$pw" "$BASE/someone-else/smoke.git/info/refs?service=git-upload-pack")" = 404 ] \
		|| fail "another owner's repository was not 404"
	# The real client, with credentials in the URL and no helper or prompt,
	# so nothing on the host is read or stored.
	export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
	remote="http://$user:$pw@127.0.0.1:$PORT/$user/smoke.git"
	git init -q -b main "$TMP/src"
	echo "pushed through the gateway" >"$TMP/src/README"
	git -C "$TMP/src" add README
	git -C "$TMP/src" -c user.name=smoke -c user.email=smoke@example.test commit -q -m "smoke push"
	git -C "$TMP/src" push -q "$remote" main >"$TMP/push.log" 2>&1 || fail "git push: $(cat "$TMP/push.log")"
	git clone -q "$remote" "$TMP/clone" >"$TMP/clone.log" 2>&1 || fail "git clone: $(cat "$TMP/clone.log")"
	[ "$(git -C "$TMP/clone" rev-parse HEAD)" = "$(git -C "$TMP/src" rev-parse HEAD)" ] || fail "the clone has another HEAD than the push"
	ok "push and clone through the gateway; no or wrong credentials 401 with a challenge; another owner 404"
fi

if [ "$WITH_TLS" -eq 1 ]; then
	echo "== 10. TLS"
	tls_port=$((PORT + 1))
	redirect_port=$((PORT + 2))
	external="https://127.0.0.1:$tls_port"
	mkdir -p "$TMP/tls"
	openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=127.0.0.1 \
		-addext "subjectAltName=IP:127.0.0.1,DNS:localhost" \
		-keyout "$TMP/tls/privkey.pem" -out "$TMP/tls/fullchain.pem" >/dev/null 2>&1 || fail "openssl could not make a certificate"
	# Readable by the gateway's uid 65532; a throwaway key, so 644 is fine here.
	chmod 755 "$TMP/tls" && chmod 644 "$TMP/tls/privkey.pem"
	cp "$ENV_FILE" "$TMP/env.tls"
	{
		echo "GIT2_TLS_DIR=$TMP/tls"
		echo "GIT2_EXTERNAL_URL=$external"
		echo "GIT2_HTTPS_PORT=$tls_port"
		echo "GIT2_HTTP_PORT=$redirect_port"
		echo "GIT2_BIND=127.0.0.1"
	} >>"$TMP/env.tls"
	dct() {
		compose --env-file "$TMP/env.tls" ${PROFILE_ARGS[@]+"${PROFILE_ARGS[@]}"} \
			-f "$COMPOSE_DIR/compose.yaml" -f "$COMPOSE_DIR/compose.tls.yaml" "$@"
	}
	dct up -d gateway >"$TMP/tls-up.log" 2>&1 || fail "up with compose.tls.yaml: $(tail -20 "$TMP/tls-up.log")"
	tcurl() { curl -sS --cacert "$TMP/tls/fullchain.pem" "$@"; }
	for _ in $(seq 1 60); do
		[ "$(tcurl -o /dev/null -w '%{http_code}' "$external/api/auth/me" 2>/dev/null || true)" = 401 ] && break
		sleep 1
	done
	[ "$(tcurl -o /dev/null -D "$TMP/headers" -w '%{http_code}' "$external/")" = 200 ] || fail "Dash not served over HTTPS"
	grep -qi '^strict-transport-security: max-age=31536000' "$TMP/headers" || fail "HTTPS response without HSTS"
	if curl -sS -o /dev/null --cacert "$TMP/tls/fullchain.pem" --tlsv1.1 --tls-max 1.1 "$external/" 2>/dev/null; then
		fail "a TLS 1.1 client was served"
	fi
	got="$(curl -sS -o /dev/null -w '%{http_code} %{redirect_url}' "http://127.0.0.1:$redirect_port/admin/users?tab=all")"
	[ "$got" = "308 $external/admin/users?tab=all" ] || fail "plain HTTP answered [$got], want a 308 to $external"
	if [ "$WITH_GIT_SVC" -eq 1 ]; then
		GIT_SSL_CAINFO="$TMP/tls/fullchain.pem" git ls-remote "https://$user:$pw@127.0.0.1:$tls_port/$user/smoke.git" >"$TMP/ls.log" 2>&1 \
			|| fail "git ls-remote over HTTPS: $(cat "$TMP/ls.log")"
		grep -q 'refs/heads/main' "$TMP/ls.log" || fail "git ls-remote over HTTPS lists no main: $(cat "$TMP/ls.log")"
	fi
	git_note=""
	[ "$WITH_GIT_SVC" -eq 1 ] && git_note="; git lists the repository over HTTPS"
	ok "HTTPS with HSTS; TLS 1.1 refused; plain HTTP 308 to $external$git_note"
fi

echo "PASS ($pass checks)"

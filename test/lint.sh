#!/bin/sh
# Static validation (U0, CA-8): compose files, shell syntax, page self-containment and
# `nginx -t` on the config exactly as the container renders it.
#
# Usage: sh test/lint.sh      Exit 0 only if every check passes. Skipped checks are printed.
#
# Deferred until the NPM integration (T3): npm/server_proxy.conf does not exist yet, so its
# `nginx -t` (wrapped in a test server{}) is skipped with a notice and starts running as soon as
# the file is added. It is also validated against the stock nginx of the backsoon image, not
# against NPM's own nginx; that comes with `make e2e`.
set -u

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT" || exit 2

ID="bs-lint-$$"
IMAGE="$ID-image:lint"
FAILED=0

cleanup() {
  trap - EXIT
  docker rm -f "$ID-nginx" "$ID-npm" >/dev/null 2>&1
  docker rmi "$IMAGE" >/dev/null 2>&1
}
trap cleanup EXIT
trap 'exit 130' INT TERM

pass() { printf 'ok    %s\n' "$1"; }
fail() {
  FAILED=1
  printf 'FAIL  %s\n' "$1"
  [ -z "${2:-}" ] || printf '%s\n' "$2" | sed 's/^/      /'
}
skip() { printf 'skip  %s\n' "$1"; }

# check LABEL COMMAND...: pass if the command exits 0, otherwise show its output.
check() {
  _label=$1
  shift
  if _out=$("$@" 2>&1); then pass "$_label"; else fail "$_label" "$_out"; fi
}

command -v docker >/dev/null 2>&1 || { echo "lint needs docker" >&2; exit 2; }

# --- compose -------------------------------------------------------------------
check "docker compose config (docker-compose.yml)" docker compose -f docker-compose.yml config -q
if [ -f compose.test.yml ]; then
  check "docker compose config (compose.test.yml, all profiles)" docker compose --profile '*' -f compose.test.yml config -q
else
  skip "compose.test.yml not present"
fi

prod=$(docker compose -f docker-compose.yml config 2>/dev/null)
serving=$(printf '%s\n' "$prod" | awk '/^  backsoon:$/ { f = 1; next } /^  [^ ]/ { f = 0 } f')
if printf '%s\n' "$prod" | grep -Eq '^ +(ports|published):'; then fail "production compose publishes no ports"; else pass "production compose publishes no ports"; fi
if printf '%s\n' "$serving" | grep -q 'restart: unless-stopped'; then pass "backsoon restarts unless-stopped"; else fail "backsoon restarts unless-stopped"; fi
if printf '%s\n' "$serving" | grep -q 'healthcheck:' && printf '%s\n' "$serving" | grep -q '/healthz'; then pass "backsoon has a /healthz healthcheck"; else fail "backsoon has a /healthz healthcheck"; fi
if printf '%s\n' "$serving" | grep -q 'read_only: true' && printf '%s\n' "$serving" | grep -q 'target: /pages'; then pass "pages volume is read-only for backsoon"; else fail "pages volume is read-only for backsoon"; fi

# --- image pin and page hygiene --------------------------------------------------
if grep -Eq '^FROM nginx:[0-9]+\.[0-9]+\.[0-9]+-alpine-slim@sha256:[0-9a-f]{64}$' nginx/Dockerfile; then pass "nginx image is pinned (version + digest)"; else fail "nginx image is pinned (version + digest)"; fi
if grep -rEn '<script src|<link |@import|url\(http' templates/; then fail "templates are self-contained (CA-14)"; else pass "templates are self-contained (CA-14)"; fi
for f in test/lint.sh test/unit.sh test/seed-race.sh test/service-lib.sh test/service-cases.sh nginx/docker-entrypoint.d/*.sh; do
  check "sh -n $f" sh -n "$f"
done

# --- nginx -t on the rendered configuration --------------------------------------
if build_out=$(docker build -q -t "$IMAGE" -f nginx/Dockerfile . 2>&1); then
  pass "docker build nginx/Dockerfile"
  if out=$(docker run --rm --name "$ID-nginx" "$IMAGE" nginx -t 2>&1); then
    pass "nginx -t (backsoon, rendered config)"
  else
    fail "nginx -t (backsoon, rendered config)" "$out"
  fi

  snippet=${BACKSOON_NPM_SNIPPET:-npm/server_proxy.conf}
  if [ -f "$snippet" ] || [ -n "${BACKSOON_NPM_SNIPPET:-}" ]; then
    # NPM defines $server and $port per proxy host; stand-ins let the snippet parse on its own.
    if out=$(
      {
        printf 'events {}\nhttp {\n  server {\n    listen 8080;\n    server_name lint.test;\n'
        printf '    set $server app1.test;\n    set $port 80;\n'
        cat "$snippet"
        printf '\n  }\n}\n'
      } | docker run --rm -i --name "$ID-npm" --entrypoint sh "$IMAGE" -c 'cat >/tmp/npm-wrapped.conf && nginx -t -c /tmp/npm-wrapped.conf' 2>&1
    ); then
      pass "nginx -t ($snippet wrapped in a test server{})"
    else
      fail "nginx -t ($snippet wrapped in a test server{})" "$out"
    fi
  else
    skip "npm/server_proxy.conf not present: NPM snippet check deferred to T3 (U3)"
  fi
else
  fail "docker build nginx/Dockerfile" "$build_out"
fi

[ "$FAILED" -eq 0 ] && echo "lint: all checks passed" || echo "lint: FAILED"
[ "$FAILED" -eq 0 ]
